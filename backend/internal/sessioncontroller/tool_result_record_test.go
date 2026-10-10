package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func toolRecordObject(t *testing.T, source string) *protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	if err := value.UnmarshalJSON([]byte(source)); err != nil {
		t.Fatal(err)
	}
	return &value
}
func TestToolResultRecordOrdinaryParallelFieldsAndIdentity(t *testing.T) {
	m := toolRecordObject(t, `{"type":"user","uuid":"results","sourceToolAssistantUUID":"assistant-b","unknownOuter":{"keep":true},"message":{"role":"user","apiExtra":3,"content":[{"type":"tool_result","tool_use_id":"call-a","content":"saved","is_error":false,"extra":{"tag":"a"}},{"type":"tool_result","tool_use_id":"call-b","content":[{"type":"text","text":"second"}],"is_error":true,"extra":{"tag":"b"}}]}}`)
	before, _ := m.MarshalJSON()
	cleaned, err := cleanRecord([]LiveRecord{{Record: m}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0].record != m {
		t.Fatal("ordinary result lost original outer identity")
	}
	after, _ := cleaned[0].record.MarshalJSON()
	if string(after) != string(before) {
		t.Fatalf("ordinary result changed: %s", after)
	}
}
func TestToolResultRecordREPLPairsMixedNativeAndVirtual(t *testing.T) {
	a := toolRecordObject(t, `{"type":"assistant","uuid":"a","isVirtual":true,"extra":"keep","message":{"role":"assistant","apiExtra":3,"content":[{"type":"tool_use","id":"r","name":"REPL","input":{}},{"type":"tool_use","id":"w","name":"Write","input":{"file_path":"fixture"}}]}}`)
	u := toolRecordObject(t, `{"type":"user","uuid":"u","isVirtual":true,"sourceToolAssistantUUID":"a","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r","content":"hidden"},{"type":"tool_result","tool_use_id":"w","content":"visible","extra":false}]}}`)
	aBefore, _ := a.MarshalJSON()
	uBefore, _ := u.MarshalJSON()
	cleaned, err := cleanRecord([]LiveRecord{{Record: a}, {Record: u}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 2 {
		t.Fatal(len(cleaned))
	}
	for _, m := range cleaned {
		b, e := m.record.MarshalJSON()
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		if strings.Contains(s, `"isVirtual"`) || strings.Contains(s, `"name":"REPL"`) || strings.Contains(s, `"tool_use_id":"r"`) {
			t.Fatal(s)
		}
	}
	b, _ := cleaned[1].record.MarshalJSON()
	if !strings.Contains(string(b), `"sourceToolAssistantUUID":"a"`) || !strings.Contains(string(b), `"extra":false`) {
		t.Fatal(string(b))
	}
	aAfter, _ := a.MarshalJSON()
	uAfter, _ := u.MarshalJSON()
	if string(aBefore) != string(aAfter) || string(uBefore) != string(uAfter) {
		t.Fatal("cleaning mutated original session")
	}
}
func TestToolResultRecordDropsREPLOnlyAndPreservesCaseSensitiveUnknownResult(t *testing.T) {
	a := toolRecordObject(t, `{"type":"assistant","uuid":"repl","message":{"role":"assistant","content":[{"type":"tool_use","id":"R","name":"REPL","input":{}}]}}`)
	u := toolRecordObject(t, `{"type":"user","uuid":"results","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"R","content":"hidden"},{"type":"tool_result","tool_use_id":"r","content":"different ID"},{"type":"tool_result","tool_use_id":"unknown","content":"no invented refusal"}]}}`)
	cleaned, e := cleanRecord([]LiveRecord{{Record: a}, {Record: u}})
	if e != nil {
		t.Fatal(e)
	}
	if len(cleaned) != 1 || cleaned[0].record.String("uuid") != "results" {
		t.Fatal(cleaned)
	}
	b, _ := cleaned[0].record.MarshalJSON()
	if strings.Contains(string(b), `"tool_use_id":"R"`) || !strings.Contains(string(b), `"tool_use_id":"r"`) || !strings.Contains(string(b), `"tool_use_id":"unknown"`) {
		t.Fatal(string(b))
	}
}
func TestToolResultRecordActualIndependentNativeAppendDedupParent(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	a := toolRecordObject(t, `{"type":"assistant","uuid":"assistant-a","message":{"role":"assistant","content":[{"type":"tool_use","id":"call-a","name":"Write","input":{}}]}}`)
	b := toolRecordObject(t, `{"type":"assistant","uuid":"assistant-b","message":{"role":"assistant","content":[{"type":"tool_use","id":"call-b","name":"Read","input":{}}]}}`)
	u := toolRecordObject(t, `{"type":"user","uuid":"parallel-results","sourceToolAssistantUUID":"assistant-a","extra":{"keep":1},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-b","content":"read","is_error":true},{"type":"tool_result","tool_use_id":"call-a","content":"written"}]}}`)
	options := RecordOptions{PromptID: func() json.RawMessage { return quote("original-turn") }}
	first := startRecord(t, owner, p, loader, []LiveRecord{{Record: a}, {Record: b}}, options)
	if _, e := first.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	history := []LiveRecord{{Record: a}, {Record: b}, {Record: u}}
	second := startRecord(t, owner, p, loader, history, options)
	if id, e := second.Wait(context.Background()); e != nil || text(id) != "parallel-results" {
		t.Fatalf("%s/%v", id, e)
	}
	if id, e := startRecord(t, owner, p, loader, history, options).Wait(context.Background()); e != nil || text(id) != "parallel-results" {
		t.Fatalf("dedup %s/%v", id, e)
	}
	if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, w)
	// The parent assertion must select the actual result row: an earlier
	// assistant row can legitimately contain the same parent UUID.
	var resultRow protocol.JSONObject
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON([]byte(line)); err != nil {
			t.Fatal(err)
		}
		if row.String("uuid") == "parallel-results" {
			resultRow = row
		}
	}
	if resultRow.String("parentUuid") != "assistant-a" {
		t.Fatalf("tool result selected wrong assistant parent: %s", resultRow.Get("parentUuid"))
	}
	if strings.Count(data, `"uuid":"parallel-results"`) != 1 || !strings.Contains(data, `"parentUuid":"assistant-a"`) || !strings.Contains(data, `"tool_use_id":"call-b","content":"read","is_error":true`) || !strings.Contains(data, `"extra":{"keep":1}`) {
		t.Fatal(data)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		cache, ok, e := p.LookupSessionUUIDCache(scope, w.options.SessionID)
		if e != nil {
			return e
		}
		if !ok {
			t.Fatal("missing actual cache")
		}
		set, e := cache.Await(scope)
		if e != nil {
			return e
		}
		has, e := set.OwnerHas(scope, quote("parallel-results"))
		if e != nil {
			return e
		}
		if !has || p.tracked != 0 {
			t.Fatal("original UUID/cache/finally lost")
		}
		return nil
	})
}
func TestToolResultRecordProviderVirtualShallowCopy(t *testing.T) {
	record := *toolRecordObject(t, `{"type":"assistant","uuid":"provider","isVirtual":true,"message":null}`)
	message := *toolRecordObject(t, `{"role":"assistant","stop_reason":null}`)
	block := &streamhistory.Block{Object: *toolRecordObject(t, `{"type":"text","text":"before"}`)}
	live := &streamhistory.Envelope{Record: record, Message: message, Content: []*streamhistory.Block{block}, UUID: "provider", Usage: &streamhistory.Usage{Raw: json.RawMessage(`{"output_tokens":0}`)}}
	cleaned, e := cleanRecord([]LiveRecord{{Assistant: live}})
	if e != nil {
		t.Fatal(e)
	}
	if len(cleaned) != 1 || cleaned[0].live == nil {
		t.Fatal("provider native view lost")
	}
	live.Record.Set("lateOuter", quote("not copied"))
	live.Message.Set("stop_reason", quote("end_turn"))
	live.Usage = &streamhistory.Usage{Raw: json.RawMessage(`{"output_tokens":9}`)}
	block.Object.Set("text", quote("before later"))
	data, e := cleaned[0].live.MarshalJSON()
	if e != nil {
		t.Fatal(e)
	}
	s := string(data)
	if strings.Contains(s, "lateOuter") || strings.Contains(s, "isVirtual") || !strings.Contains(s, `"stop_reason":null`) || !strings.Contains(s, `"output_tokens":0`) || !strings.Contains(s, "before later") {
		t.Fatal(s)
	}
}
func TestToolResultRecordInvalidOriginalRefusesBeforeNativeEffects(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	m := plainMessage("dual", "invalid")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		f, e := p.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: m, Assistant: &streamhistory.Envelope{}}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if e == nil || f != nil {
			t.Fatal("invalid dual original admitted")
		}
		return nil
	})
	if _, e := os.Stat(w.target()); !os.IsNotExist(e) {
		t.Fatal("invalid input reached native transcript", e)
	}
}

func TestToolResultRecordFullSessionIncrementalAndExplicitEmpty(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	repl := toolRecordObject(t, `{"type":"assistant","uuid":"earlier-repl","message":{"role":"assistant","content":[{"type":"tool_use","id":"r","name":"REPL","input":{}}]}}`)
	user := toolRecordObject(t, `{"type":"user","uuid":"incremental","sourceToolAssistantUUID":"ordinary-assistant","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r","content":"hidden"},{"type":"tool_result","tool_use_id":"ordinary","content":"visible"}]}}`)
	options := RecordOptions{PromptID: func() json.RawMessage { return nil }, AllMessages: []LiveRecord{{Record: repl}, {Record: user}}}
	if id, e := startRecord(t, owner, p, loader, []LiveRecord{{Record: user}}, options).Wait(context.Background()); e != nil || text(id) != "incremental" {
		t.Fatalf("%s/%v", id, e)
	}
	orphan := toolRecordObject(t, `{"type":"user","uuid":"explicit-empty","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r","content":"retain"}]}}`)
	options.AllMessages = []LiveRecord{}
	if _, e := startRecord(t, owner, p, loader, []LiveRecord{{Record: orphan}}, options).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, w)
	if strings.Contains(data, `"content":"hidden"`) || !strings.Contains(data, `"content":"visible"`) || !strings.Contains(data, `"content":"retain"`) {
		t.Fatal(data)
	}
}
func TestToolResultRecordSynchronousCallersShareCleaning(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-Record", true: "RecordLive"}[live], func(t *testing.T) {
			owner, w, pending, _ := recordHost(t, OSFiles{}, nil)
			a := toolRecordObject(t, `{"type":"assistant","uuid":"a","message":{"role":"assistant","content":[{"type":"tool_use","id":"r","name":"REPL","input":{}}]}}`)
			u := toolRecordObject(t, `{"type":"user","uuid":"u","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r","content":"hidden"},{"type":"tool_result","tool_use_id":"w","content":"visible"}]}}`)
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				var e error
				if live {
					_, e = w.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: a}, {Record: u}}, nil)
				} else {
					_, e = w.Record([]*protocol.JSONObject{a, u}, nil)
				}
				return e
			})
			// This writer is bound to the existing independent native drain owner.
			if err := flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			data := recordBytes(t, w)
			if strings.Contains(data, `"uuid":"a"`) || strings.Contains(data, "hidden") || !strings.Contains(data, "visible") {
				t.Fatal(data)
			}
		})
	}
}
func TestToolResultRecordPreservesUTF16ToolIDIdentity(t *testing.T) {
	a := toolRecordObject(t, `{"type":"assistant","uuid":"a","message":{"role":"assistant","content":[{"type":"tool_use","id":"\ud800","name":"REPL","input":{}}]}}`)
	u := toolRecordObject(t, `{"type":"user","uuid":"u","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"\ud800","content":"hide"},{"type":"tool_result","tool_use_id":"\ud801","content":"retain"}]}}`)
	values, e := cleanRecord([]LiveRecord{{Record: a}, {Record: u}})
	if e != nil {
		t.Fatal(e)
	}
	if len(values) != 1 {
		t.Fatal(len(values))
	}
	b, _ := values[0].record.MarshalJSON()
	if strings.Contains(string(b), "hide") || !strings.Contains(string(b), `"tool_use_id":"\ud801"`) {
		t.Fatal(string(b))
	}
}
func TestToolResultRecordNullBlockOriginalRejectedPromiseBeforeFS(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	m := toolRecordObject(t, `{"type":"user","uuid":"null-block","message":{"role":"user","content":[null]}}`)
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		future, e := p.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: m}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if e != nil || future == nil {
			return fmt.Errorf("original null block did not return rejected Record promise: %v", e)
		}
		_, e = future.Await(scope)
		var native *LoggingTypeError
		if !errors.As(e, &native) {
			return fmt.Errorf("null block promise wrong error: %T %v", e, e)
		}
		return nil
	})
	if _, e := os.Stat(w.target()); !os.IsNotExist(e) {
		t.Fatal("invalid block reached native filesystem", e)
	}
}
