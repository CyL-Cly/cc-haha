package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type batchHarness struct {
	Tool                 RegisteredTool
	Capabilities         ToolBatchCapabilities
	Factory              *messages.Factory
	Uses                 []Invocation
	Order                []string
	Calls, Parses, UUIDs int
	Emit                 func(Message) error
	ctx                  context.Context
}

func newBatchHarness(t *testing.T) *batchHarness {
	t.Helper()
	h := &batchHarness{ctx: context.Background()}
	h.Factory, _ = messages.NewFactory(func() string { h.UUIDs++; h.Order = append(h.Order, "uuid"); return fmt.Sprint(h.UUIDs) }, func() time.Time { h.Order = append(h.Order, "time"); return time.UnixMilli(1700000000000) })
	h.Tool = RegisteredTool{Name: "Write", Aliases: []string{"LegacyWrite"}, PersistenceThreshold: 1000,
		ParseInput:        func(input protocol.JSONObject) (protocol.JSONObject, error) { h.Parses++; return input.Clone(), nil },
		IsConcurrencySafe: func(protocol.JSONObject) bool { return false },
		ValidateInput: func(context.Context, protocol.JSONObject, any) error {
			h.Order = append(h.Order, "validate")
			return nil
		},
		Call: func(_ context.Context, input protocol.JSONObject, call ToolCallContext) (ToolCallResult, error) {
			h.Calls++
			h.Order = append(h.Order, "call:"+input.String("file_path"))
			return ToolCallResult{Data: quoted("written")}, nil
		},
		MapResult: func(data json.RawMessage, id string) (protocol.JSONObject, error) {
			h.Order = append(h.Order, "map")
			return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: quoted(id)}, protocol.JSONField{Name: "content", Value: data}), nil
		},
	}
	h.Capabilities = ToolBatchCapabilities{
		PreTool: func(_ context.Context, _ RegisteredTool, input *protocol.JSONObject, _ ToolCallContext) (*protocol.JSONObject, error) {
			h.Order = append(h.Order, "pre")
			return input, nil
		},
		ResolvePermission: func(_ context.Context, _ RegisteredTool, input *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
			h.Order = append(h.Order, "permission")
			return ToolPermission{Behavior: "allow", Input: input}, nil
		},
		PostTool: func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
			h.Order = append(h.Order, "post")
			return nil, nil
		},
		InProgress: func(id string, active bool) error {
			h.Order = append(h.Order, fmt.Sprintf("progress:%s:%t", id, active))
			return nil
		},
	}
	h.Uses = []Invocation{{Block: &streamhistory.Block{Object: fixtureObject(`{"type":"tool_use","id":"call","name":"Write","input":{"file_path":"original.txt","content":"text"}}`)}, Assistant: &streamhistory.Envelope{UUID: "original-assistant"}}}
	h.Emit = func(Message) error { h.Order = append(h.Order, "emit"); return nil }
	return h
}
func (h *batchHarness) run() (any, error) {
	batch, err := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if err != nil {
		return nil, err
	}
	return batch.Execute(h.ctx, Round{ToolContext: "original", History: []Message{{Record: fixtureObject(`{"type":"user","message":{"role":"user","content":"original history"}}`)}}, Tracking: Tracking{"original-chain", 3}}, h.Uses, h.Emit)
}
func TestSerialToolBatchHasNoImplicitCapabilitiesOrRegisteredToolDefaults(t *testing.T) {
	for _, mutate := range []func(*batchHarness){
		func(h *batchHarness) { h.Factory = nil }, func(h *batchHarness) { h.Capabilities.PreTool = nil }, func(h *batchHarness) { h.Capabilities.ResolvePermission = nil }, func(h *batchHarness) { h.Capabilities.PostTool = nil }, func(h *batchHarness) { h.Capabilities.InProgress = nil },
		func(h *batchHarness) { h.Tool.Name = "" }, func(h *batchHarness) { h.Tool.ParseInput = nil }, func(h *batchHarness) { h.Tool.IsConcurrencySafe = nil }, func(h *batchHarness) { h.Tool.Call = nil }, func(h *batchHarness) { h.Tool.MapResult = nil }, func(h *batchHarness) { h.Tool.PersistenceThreshold = 0 },
		func(h *batchHarness) { h.Tool.Name = "Bash" }, func(h *batchHarness) { h.Tool.IsMCP = true }, func(h *batchHarness) { h.Tool.Name = "mcp__fixture__write" },
	} {
		h := newBatchHarness(t)
		mutate(h)
		if _, err := h.run(); err == nil || h.Calls != 0 || h.UUIDs != 0 {
			t.Fatalf("missing capability accepted or advanced: %v %+v", err, h.Order)
		}
	}
}
func TestSerialPartitionRejectsUnportedBatchesBeforePermissionOrToolSideEffects(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(*batchHarness)
	}{
		{"unregistered", func(h *batchHarness) { h.Uses[0].Block.Object.Set("name", quoted("missing")) }},
		{"missing assistant", func(h *batchHarness) { h.Uses[0].Assistant = nil }},
		{"missing block", func(h *batchHarness) { h.Uses[0].Block = nil }},
		{"null input", func(h *batchHarness) { h.Uses[0].Block.Object.Set("input", json.RawMessage(`null`)) }},
		{"invalid schema", func(h *batchHarness) {
			h.Tool.ParseInput = func(protocol.JSONObject) (protocol.JSONObject, error) {
				return protocol.JSONObject{}, errors.New("schema invalid")
			}
		}},
		{"concurrent", func(h *batchHarness) { h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true } }},
		{"no consumer", func(h *batchHarness) { h.Emit = nil }},
	} {
		t.Run(item.name, func(t *testing.T) {
			h := newBatchHarness(t)
			item.mutate(h)
			_, err := h.run()
			wantOrder := []string(nil)
			if item.name == "unregistered" || item.name == "null input" || item.name == "invalid schema" {
				wantOrder = []string{"progress:call:true"}
			}
			if err == nil || !reflect.DeepEqual(h.Order, wantOrder) || h.Calls != 0 || h.UUIDs != 0 {
				t.Fatalf("unported partition advanced: %v %v", err, h.Order)
			}
		})
	}
}

func TestModelToolIdentityCannotCollapseUTF16BeforeSideEffects(t *testing.T) {
	for _, item := range []struct{ name, field, raw string }{

		{"high id", "id", `"\ud800"`}, {"low id", "id", `"\udfff"`},
		{"non-string name", "name", `null`}, {"non-string id", "id", `3`},
		{"paired prefix then lone", "id", `"\ud83d\ude00\ud800"`},
	} {
		t.Run(item.name, func(t *testing.T) {
			h := newBatchHarness(t)
			h.Tool.Aliases = append(h.Tool.Aliases, "�")
			h.Uses[0].Block.Object.Set(item.field, json.RawMessage(item.raw))
			if _, err := h.run(); !errors.Is(err, ErrUnsupported) || h.Parses != 0 || len(h.Order) != 0 || h.Calls != 0 || h.UUIDs != 0 {
				t.Fatalf("lossy identity advanced: err=%v parses=%d calls=%d effects=%v", err, h.Parses, h.Calls, h.Order)
			}
		})
	}
	h := newBatchHarness(t)
	second := *h.Uses[0].Block
	second.Object = second.Object.Clone()
	second.Object.Set("id", json.RawMessage(`"\ud800"`))
	h.Uses = append(h.Uses, Invocation{Block: &second, Assistant: h.Uses[0].Assistant})
	if _, err := h.run(); !errors.Is(err, ErrUnsupported) || h.Calls != 0 || len(h.Order) != 0 {
		t.Fatalf("later unrepresentable ID executed an earlier tool: %v %v", err, h.Order)
	}
}

func TestObservableBackfillCannotMatchReplacementCharacterForLoneNameOrID(t *testing.T) {
	for _, item := range []struct{ field, raw string }{{"name", `"\ud800"`}, {"name", `"\udfff"`}, {"id", `"\ud800"`}} {
		block := &streamhistory.Block{Object: fixtureObject(`{"type":"tool_use","id":"call","name":"�","input":{}}`)}
		block.Object.Set(item.field, json.RawMessage(item.raw))
		lookups, backfills := 0, 0
		_, err := observableEnvelope(&streamhistory.Envelope{Content: []*streamhistory.Block{block}}, func(name string) (Backfill, error) {
			lookups++
			if name != "�" {
				t.Fatalf("name changed %q", name)
			}
			return func(*protocol.JSONObject) error { backfills++; return nil }, nil
		})
		want := 0
		if item.field == "id" {
			want = 1
		}
		if err != nil || lookups != want || backfills != want {
			t.Fatalf("identity %s=%s: %v lookups=%d backfills=%d", item.field, item.raw, err, lookups, backfills)
		}
	}
}

func TestExecutionRechecksIdentityAfterEarlierHostMutation(t *testing.T) {
	for _, item := range []struct{ field, raw string }{{"id", `"\ud800"`}, {"name", `"\udfff"`}, {"name", `"unknown-after-callback"`}, {"id", `null`}, {"name", `2`}} {
		h := newBatchHarness(t)
		second := *h.Uses[0].Block
		second.Object = second.Object.Clone()
		second.Object.Set("id", quoted("second"))
		h.Uses = append(h.Uses, Invocation{&second, h.Uses[0].Assistant})
		h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
			h.Calls++
			second.Object.Set(item.field, json.RawMessage(item.raw))
			return ToolCallResult{Data: quoted("written")}, nil
		}
		_, err := h.run()
		if !errors.Is(err, ErrUnsupported) || h.Calls != 1 || h.UUIDs != 1 {
			t.Fatalf("mutated identity executed: %v calls=%d allocations=%d", err, h.Calls, h.UUIDs)
		}
		for _, effect := range h.Order {
			if effect == "progress::true" || effect == "progress:�:true" {
				t.Fatalf("mutated identity reached host: %v", h.Order)
			}
		}
	}
}

func TestScalarToolNameAndIDKeepEscapeAndEmojiIdentity(t *testing.T) {
	for _, item := range []struct{ name, id, registry, wantID string }{
		{`"\ufffd"`, `"\ufffd"`, "�", "�"},
		{`"\ud83d\ude00"`, `"😀"`, "😀", "😀"},
		{`"😀"`, `"\ud83d\ude00"`, "😀", "😀"},
		{`"LegacyWrite"`, `"a\u0062"`, "Write", "ab"},
	} {
		h := newBatchHarness(t)
		h.Tool.Name = item.registry
		h.Uses[0].Block.Object.Set("name", json.RawMessage(item.name))
		h.Uses[0].Block.Object.Set("id", json.RawMessage(item.id))
		call, mapped := "", ""
		h.Tool.Call = func(_ context.Context, _ protocol.JSONObject, context ToolCallContext) (ToolCallResult, error) {
			h.Calls++
			call = context.ToolUseID
			return ToolCallResult{Data: quoted("written")}, nil
		}
		h.Tool.MapResult = func(data json.RawMessage, id string) (protocol.JSONObject, error) {
			mapped = id
			return fixtureObject(`{"type":"tool_result","content":"written"}`), nil
		}
		if _, err := h.run(); err != nil || h.Calls != 1 || call != item.wantID || mapped != item.wantID {
			t.Fatalf("scalar identity changed: %v calls=%d call=%q map=%q", err, h.Calls, call, mapped)
		}
	}
}

func TestControllerKeepsActualSourceUTF16IDDedupeBeforeExecutionBoundary(t *testing.T) {
	// Reuse the committed production queryModelWithStreaming recording, not
	// a Go-created expected ID list. Its four IDs include high, low, U+FFFD
	// and an emoji, while replayed high/escaped emoji stops are suppressed.
	raw, err := os.ReadFile("../streamhistory/testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Name        string
			Observation struct {
				Wire       string
				Assistants []struct {
					Message string `json:"messageBytesAfterStream"`
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, item := range corpus.Cases {
		if item.Name != "tool-id-utf16-distinct-and-replay" {
			continue
		}
		want := []string{}
		for _, row := range item.Observation.Assistants {
			message := fixtureObject(row.Message)
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
				t.Fatal(err)
			}
			for _, block := range blocks {
				id, err := protocol.ParseAndStringifyJavaScriptJSON(block.Get("id"))
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, string(id))
			}
		}
		h := newBatchHarness(t)
		h.Tool.Name = "FixtureTool"
		h.Uses = nil
		controller, err := streamhistory.New(streamhistory.Options{UUID: func() string { return "test-assistant" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		emit := func(update streamhistory.Update) error {
			if update.Envelope != nil {
				for _, block := range update.Envelope.Content {
					id, err := protocol.ParseAndStringifyJavaScriptJSON(block.Object.Get("id"))
					if err != nil {
						return err
					}
					got = append(got, string(id))
					h.Uses = append(h.Uses, Invocation{block, update.Envelope})
				}
			}
			return nil
		}
		decoder := protocol.NewSDKSSEDecoder(strings.NewReader(item.Observation.Wire))
		for {
			part, err := decoder.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := controller.Feed(json.RawMessage(part.Data), streamhistory.Timing{}, emit); err != nil {
				t.Fatal(err)
			}
		}
		if err := controller.Finish(nil, emit); err != nil {
			t.Fatal(err)
		}
		if len(want) != 4 || !reflect.DeepEqual(got, want) {
			t.Fatalf("raw ID dedupe changed: got=%v Source=%v", got, want)
		}
		if _, err := h.run(); !errors.Is(err, ErrUnsupported) || h.Calls != 0 || len(h.Order) != 0 {
			t.Fatalf("Controller's lone IDs escaped execution guard: %v %v", err, h.Order)
		}
		return
	}
	t.Fatal("missing actual Source UTF-16 ID corpus")
}
func TestSerialSuccessPreservesSourceAllocationModifierEmissionAndCompletionOrder(t *testing.T) {
	h := newBatchHarness(t)
	h.Uses[0].Block.Object.Set("name", quoted("LegacyWrite"))
	second := *h.Uses[0].Block
	second.Object = second.Object.Clone()
	second.Object.Set("id", quoted("second"))
	h.Uses = append(h.Uses, Invocation{Block: &second, Assistant: h.Uses[0].Assistant})
	h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { panic("shell quoting failed") }
	h.Tool.Call = func(_ context.Context, input protocol.JSONObject, call ToolCallContext) (ToolCallResult, error) {
		h.Calls++
		if len(call.Messages) != 1 || call.Messages[0].Type() != "user" || call.QueryTracking != (Tracking{"original-chain", 3}) || call.Invocation.Assistant.UUID != "original-assistant" {
			t.Fatal("resolved original history/chain/assistant not passed to registered tool")
		}
		h.Order = append(h.Order, fmt.Sprintf("call-context:%v", call.Opaque))
		return ToolCallResult{Data: quoted("ok"), ModifyContext: func(value any) (any, error) {
			h.Order = append(h.Order, "modify")
			return value.(string) + "-next", nil
		}}, nil
	}
	h.Capabilities.PostTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
		h.Order = append(h.Order, "post")
		return []Message{{Record: fixtureObject(`{"type":"progress"}`)}}, nil
	}
	result, err := h.run()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"progress:call:true", "validate", "pre", "permission", "call-context:original", "map", "uuid", "time", "post", "modify", "emit", "emit", "progress:call:false", "progress:second:true", "validate", "pre", "permission", "call-context:original-next", "map", "uuid", "time", "post", "modify", "emit", "emit", "progress:second:false"}
	if !reflect.DeepEqual(h.Order, want) || result != "original-next-next" || h.Parses != 4 || h.Calls != 2 || h.UUIDs != 2 {
		t.Fatalf("serial Source order: %v context=%v parses=%d", h.Order, result, h.Parses)
	}
	batch, _ := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if backfill, err := batch.LookupBackfill("absent"); backfill != nil || err != nil {
		t.Fatalf("observable unknown tool changed: %v %v", backfill, err)
	}
	h.Tool.Backfill = func(*protocol.JSONObject) error { return nil }
	batch, _ = NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if backfill, err := batch.LookupBackfill("LegacyWrite"); backfill == nil || err != nil {
		t.Fatalf("registry alias not used: %v", err)
	}
}
func TestToolCallInputConvergencePreservesHostUpdatesAndOriginalModelPath(t *testing.T) {
	for _, item := range []struct {
		name     string
		backfill bool
		replace  string
		want     string
	}{
		{"unchanged", false, "", `{"file_path":"original.txt","content":"text"}`},
		{"added no path", true, "no-path", `{"content":"text","legacy":"added"}`},
		{"restore backfilled path", true, "", `{"file_path":"original.txt","content":"text","legacy":"added"}`},
		{"intentional permission path", true, "different-path", `{"file_path":"approved.txt","content":"text","legacy":"added"}`},
		{"permission content", true, "content", `{"file_path":"original.txt","content":"approved","legacy":"added"}`},
		{"pre-hook replacement", true, "pre", `{"file_path":"original.txt","content":"pre-hook","legacy":"added"}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			h := newBatchHarness(t)
			before := fixtureBytes(h.Uses[0].Block.Object)
			if item.backfill {
				h.Tool.Backfill = func(value *protocol.JSONObject) error {
					value.Set("file_path", quoted("expanded.txt"))
					value.Set("legacy", quoted("added"))
					return nil
				}
			}
			if item.replace == "pre" {
				h.Capabilities.PreTool = func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ ToolCallContext) (*protocol.JSONObject, error) {
					copy := value.Clone()
					copy.Set("content", quoted("pre-hook"))
					return &copy, nil
				}
			}
			h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
				decision := ToolPermission{Behavior: "allow", Input: value, UserModified: true}
				copy := value.Clone()
				switch item.replace {
				case "no-path":
					copy.Delete("file_path")
					decision.UpdatedInput = &copy
				case "different-path":
					copy.Set("file_path", quoted("approved.txt"))
					decision.UpdatedInput = &copy
				case "content":
					copy.Set("content", quoted("approved"))
					decision.UpdatedInput = &copy
				}
				return decision, nil
			}
			h.Tool.Call = func(_ context.Context, value protocol.JSONObject, call ToolCallContext) (ToolCallResult, error) {
				if fixtureBytes(value) != item.want || !call.UserModified {
					t.Fatalf("callInput=%s userModified=%t", fixtureBytes(value), call.UserModified)
				}
				return ToolCallResult{Data: quoted("ok")}, nil
			}
			if _, err := h.run(); err != nil {
				t.Fatal(err)
			}
			if fixtureBytes(h.Uses[0].Block.Object) != before {
				t.Fatal("processing mutated original assistant history")
			}
		})
	}
	original := fixtureObject(`{"file_path":"\u0078\ud800"}`)
	expanded := fixtureObject(`{"file_path":"x\ud800"}`)
	if !equalString(original.Get("file_path"), expanded.Get("file_path")) {
		t.Fatal("UTF16 equality changed")
	}
}
func TestToolPhaseFailuresRetainOwnershipAndNeverExecuteARejectedTool(t *testing.T) {
	boom := errors.New("host sentinel")
	for _, item := range []struct {
		name       string
		mutate     func(*batchHarness)
		calls, ids int
		scoped     bool
	}{
		{"add progress", func(h *batchHarness) { h.Capabilities.InProgress = func(string, bool) error { return boom } }, 0, 0, false},
		{"caller canceled", func(h *batchHarness) { ctx, cancel := context.WithCancel(context.Background()); cancel(); h.ctx = ctx }, 0, 0, true},
		{"second parse", func(h *batchHarness) {
			h.Tool.ParseInput = func(value protocol.JSONObject) (protocol.JSONObject, error) {
				h.Parses++
				if h.Parses == 2 {
					return protocol.JSONObject{}, boom
				}
				return value, nil
			}
		}, 0, 0, true},
		{"value validation", func(h *batchHarness) {
			h.Tool.ValidateInput = func(context.Context, protocol.JSONObject, any) error { return boom }
		}, 0, 0, true},
		{"backfill", func(h *batchHarness) { h.Tool.Backfill = func(*protocol.JSONObject) error { return boom } }, 0, 0, false},
		{"pre-hook", func(h *batchHarness) {
			h.Capabilities.PreTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (*protocol.JSONObject, error) {
				return nil, boom
			}
		}, 0, 0, false},
		{"nil pre-input", func(h *batchHarness) {
			h.Capabilities.PreTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (*protocol.JSONObject, error) {
				return nil, nil
			}
		}, 0, 0, true},
		{"permission failure", func(h *batchHarness) {
			h.Capabilities.ResolvePermission = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (ToolPermission, error) {
				return ToolPermission{}, boom
			}
		}, 0, 0, false},
		{"deny permission", func(h *batchHarness) {
			h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
				return ToolPermission{Behavior: "deny", Input: value}, nil
			}
		}, 0, 0, true},
		{"missing resolved input", func(h *batchHarness) {
			h.Capabilities.ResolvePermission = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (ToolPermission, error) {
				return ToolPermission{Behavior: "allow"}, nil
			}
		}, 0, 0, true},
		{"null updated input", func(h *batchHarness) {
			h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
				empty := protocol.JSONObject{}
				return ToolPermission{Behavior: "allow", Input: value, UpdatedInput: &empty}, nil
			}
		}, 0, 0, true},
		{"tool failure", func(h *batchHarness) {
			h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
				h.Calls++
				return ToolCallResult{}, boom
			}
		}, 1, 0, true},
		{"non-string backfilled path", func(h *batchHarness) {
			h.Tool.Backfill = func(value *protocol.JSONObject) error { value.Set("file_path", json.RawMessage(`null`)); return nil }
		}, 0, 0, true},
		{"map failure", func(h *batchHarness) {
			h.Tool.MapResult = func(json.RawMessage, string) (protocol.JSONObject, error) { return protocol.JSONObject{}, boom }
		}, 1, 0, false},
		{"persistence required", func(h *batchHarness) { h.Tool.PersistenceThreshold = 1 }, 1, 0, true},
		{"mapped invalid JSON", func(h *batchHarness) {
			h.Tool.MapResult = func(json.RawMessage, string) (protocol.JSONObject, error) {
				value := fixtureObject(`{"type":"tool_result","content":"ok"}`)
				value.Set("future", json.RawMessage(`{`))
				return value, nil
			}
		}, 1, 0, false},
		{"post-hook", func(h *batchHarness) {
			h.Capabilities.PostTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
				return nil, boom
			}
		}, 1, 1, false},
		{"context modifier", func(h *batchHarness) {
			h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
				h.Calls++
				return ToolCallResult{Data: quoted("ok"), ModifyContext: func(any) (any, error) { return nil, boom }}, nil
			}
		}, 1, 1, false},
		{"consumer", func(h *batchHarness) { h.Emit = func(Message) error { return boom } }, 1, 1, false},
		{"post message consumer", func(h *batchHarness) {
			h.Capabilities.PostTool = func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error) {
				return []Message{{Record: fixtureObject(`{"type":"progress"}`)}}, nil
			}
			count := 0
			h.Emit = func(Message) error {
				count++
				if count == 2 {
					return boom
				}
				return nil
			}
		}, 1, 1, false},
		{"complete progress", func(h *batchHarness) {
			h.Capabilities.InProgress = func(_ string, active bool) error {
				if !active {
					return boom
				}
				return nil
			}
		}, 1, 1, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			h := newBatchHarness(t)
			item.mutate(h)
			_, err := h.run()
			if err == nil || h.Calls != item.calls || h.UUIDs != item.ids {
				t.Fatalf("failure advanced: %v calls=%d ids=%d order=%v", err, h.Calls, h.UUIDs, h.Order)
			}
			if item.scoped != errors.Is(err, ErrUnsupported) {
				t.Fatalf("failure ownership changed: %v", err)
			}
			if !item.scoped && item.name != "mapped invalid JSON" && err != boom {
				t.Fatalf("host/consumer error replaced: %v", err)
			}
		})
	}
}

func TestSmallResultsUseUTF16SizeWhitespaceMarkerAndFailExplicitlyForPersistence(t *testing.T) {
	for _, item := range []struct {
		content, want string
		threshold     float64
	}{
		{`null`, `"(Write completed with no output)"`, 1}, {`"\ufeff\u2003\t"`, `"(Write completed with no output)"`, 1}, {`[]`, `"(Write completed with no output)"`, 1}, {`[{"type":"text","text":"\n"}]`, `"(Write completed with no output)"`, 1},
		{`"\ud800"`, `"\ud800"`, 1}, {`"😀"`, `"😀"`, 2}, {`[{"type":"tool_reference","tool_name":"Write"}]`, `[{"type":"tool_reference","tool_name":"Write"}]`, 1},
		{`[{"type":"image"}]`, `[{"type":"image"}]`, 10},
	} {
		block := fixtureObject(`{"type":"tool_result","future":1}`)
		block.Set("content", json.RawMessage(item.content))
		actual, err := ProcessSmallTextToolResult(block, "Write", item.threshold)
		if err != nil || string(actual.Get("content")) != item.want {
			t.Fatalf("content=%s result=%s error=%v", item.content, actual.Get("content"), err)
		}
	}
	for _, item := range []struct {
		content   string
		threshold float64
	}{{`"😀"`, 1}, {`{"future":true}`, 1}, {`[{"type":"text","text":null}]`, 10}, {`[`, 10}} {
		block := fixtureObject(`{"type":"tool_result"}`)
		block.Set("content", json.RawMessage(item.content))
		if _, err := ProcessSmallTextToolResult(block, "Write", item.threshold); err == nil {
			t.Fatalf("unsupported result accepted: %s", item.content)
		}
	}
	if _, err := ProcessSmallTextToolResult(fixtureObject(`{}`), "", 0); err == nil {
		t.Fatal("invalid mapped result accepted")
	}
	block := fixtureObject(`{"type":"tool_result"}`)
	actual, err := ProcessSmallTextToolResult(block, "Write", 1)
	if err != nil || actual.String("content") != "(Write completed with no output)" {
		t.Fatalf("missing result marker: %v", err)
	}
}
func TestOrdinaryResultNormalizationClonesOnlyWhenStrippingAndNeverLeaksReferences(t *testing.T) {
	message := Message{Record: fixtureObject(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"tool_reference","tool_name":"Write"},{"type":"text","text":"keep\ud800"}]},{"type":"tool_result","tool_use_id":"second","content":[]},{"type":"text","text":"tail"}]},"uuid":"original","future":"\udfff"}`)}
	before, _ := message.MarshalJSON()
	actual, err := NormalizeOrdinaryToolResult(message)
	if err != nil || len(actual) != 1 {
		t.Fatal(err)
	}
	raw, _ := actual[0].MarshalJSON()
	want := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"text","text":"keep\ud800"}]},{"type":"tool_result","tool_use_id":"second","content":[{"type":"text","text":"[Tool references removed - tool search not enabled]"}]},{"type":"text","text":"tail"}]},"uuid":"original","future":"\udfff"}`
	if string(raw) != want {
		t.Fatalf("normalization=%s", raw)
	}
	after, _ := message.MarshalJSON()
	if string(before) != string(after) {
		t.Fatal("normalizer mutated observable output")
	}
	for _, source := range []string{`{"type":"user","message":{"role":"user","content":"plain"}}`, `{"type":"user","isVirtual":false,"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`} {
		value := Message{Record: fixtureObject(source)}
		items, err := NormalizeOrdinaryToolResult(value)
		if err != nil || len(items) != 1 || items[0].Record.Get("message") == nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{`{"type":"progress"}`, `{"type":"system"}`, `{"type":"user","isVirtual":true}`} {
		items, err := NormalizeOrdinaryToolResult(Message{Record: fixtureObject(source)})
		if err != nil || len(items) != 0 {
			t.Fatalf("display-only marker entered history: %v", err)
		}
	}
	for _, source := range []string{`{"type":"system","subtype":"local_command"}`, `{"type":"attachment"}`, `{"type":"user","isVirtual":1}`, `{"type":"user","message":null}`, `{"type":"user","message":{"content":null}}`, `{"type":"user","message":{"content":[{"type":"image"}]}}`, `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"image"}]}]}}`} {
		if _, err := NormalizeOrdinaryToolResult(Message{Record: fixtureObject(source)}); err == nil {
			t.Fatalf("unported normalization accepted: %s", source)
		}
	}
}
func TestOrderedFactoryRetainsSourceMetadataEnumerationAndSharedFactoryDefaults(t *testing.T) {
	h := newBatchHarness(t)
	value := CreateOrderedUserMessage(h.Factory, storage.Object{"content": quoted(""), "isMeta": json.RawMessage(`true`), "toolUseResult": json.RawMessage(`null`), "origin": json.RawMessage(`{"future":"\ud800"}`)})
	raw, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"user","message":{"role":"user","content":"(no content)"},"isMeta":true,"uuid":"1","timestamp":"2023-11-14T22:13:20.000Z","toolUseResult":null,"origin":{"future":"\ud800"}}`
	if string(raw) != want {
		t.Fatalf("ordered shared factory=%s", raw)
	}
}
