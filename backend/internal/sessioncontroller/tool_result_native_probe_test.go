package sessioncontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Native probe inputs are the authored original programs, never Source outputs.
// It observes this package's real Record service and physical persistence. The
// raw JSONObject inner-value boundary is reported honestly by late comparison;
// this does not claim an actual provider, CLI tool execution or resume boundary.
func TestToolResultRecordNativeProgram(t *testing.T) {
	name := os.Getenv("CC_HAHA_RECORD_NATIVE_INPUT")
	if name == "" {
		t.Skip("only the explicitly frozen native-program runner")
	}
	home := os.Getenv("HOME")
	if filepath.Dir(filepath.Dir(name)) != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("USERPROFILE") != home || os.Getenv("TMPDIR") != filepath.Join(home, "tmp") {
		t.Fatal("explicit private fixture paths required")
	}
	var input struct {
		ID          string            `json:"id"`
		Messages    []json.RawMessage `json:"messages"`
		AllMessages []json.RawMessage `json:"allMessages"`
		Mutation    *struct {
			Index int `json:"index"`
		} `json:"mutation"`
	}
	bytes, e := os.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(bytes, &input); e != nil {
		t.Fatal(e)
	}
	decode := func(values []json.RawMessage) []LiveRecord {
		if values == nil {
			return nil
		}
		r := make([]LiveRecord, 0, len(values))
		for _, raw := range values {
			m := toolRecordObject(t, string(raw))
			r = append(r, LiveRecord{Record: m})
		}
		return r
	}
	messages := decode(input.Messages)
	all := decode(input.AllMessages)
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	result := map[string]any{"id": input.ID, "PID": os.Getpid(), "HOME": home, "sourceOutputsRuntimeInputs": false, "scope": "external raw original-message Record service only; original raw-inner mutation differences retained"}
	serialize := func(values []recordOriginal) string {
		records := make([]protocol.JSONObject, 0, len(values))
		for _, value := range values {
			records = append(records, *value.record)
		}
		b, e := json.Marshal(records)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	var future *RecordFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		cleaned, err := cleanRecordWithAll(messages, all)
		if err != nil {
			name := fmt.Sprintf("%T", err)
			if named, ok := err.(interface{ ErrorName() string }); ok {
				name = named.ErrorName()
			}
			result["error"] = map[string]any{"name": name, "message": err.Error(), "actualNativeGoType": fmt.Sprintf("%T", err)}
			// Exercise the real original Record promise at the same error input,
			// instead of counting a synchronous helper refusal as persistence.
			future, e = p.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, messages, RecordOptions{PromptID: func() json.RawMessage { return nil }, AllMessages: all})
			return e
		}
		result["cleanedBefore"] = serialize(cleaned)
		originals := make([]int, 0, len(cleaned))
		for _, value := range cleaned {
			index := -1
			for i, m := range messages {
				if value.record == m.Record {
					index = i
				}
			}
			originals = append(originals, index)
		}
		result["outerOriginalIndexes"] = originals
		if input.Mutation != nil {
			m := messages[input.Mutation.Index].Record
			m.Set("lateOuter", quote("original input control"))
			api, ok := m.Object("message")
			if !ok {
				return fmt.Errorf("original mutation message is not an object")
			}
			api.Set("stop_reason", quote("end_turn"))
			blocks, ok := api.Array("content")
			if !ok {
				return fmt.Errorf("original mutation content is not an array")
			}
			for i := 0; i < blocks.Len(); i++ {
				block, ok := blocks.Object(i)
				if ok && block.String("type") == "text" {
					block.Set("text", quote(block.String("text")+" later"))
				}
			}
		}
		result["cleanedAfter"] = serialize(cleaned)
		result["inputAfter"] = serialize(func() []recordOriginal {
			r := make([]recordOriginal, 0, len(messages))
			for _, m := range messages {
				r = append(r, recordOriginal{record: m.Record})
			}
			return r
		}())
		future, e = p.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, messages, RecordOptions{PromptID: func() json.RawMessage { return nil }, AllMessages: all})
		return e
	})
	if future != nil {
		id, err := future.Wait(context.Background())
		result["recordUUID"] = string(id)
		if err != nil {
			result["recordError"] = map[string]any{"name": fmt.Sprintf("%T", err), "message": err.Error()}
		}
		if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if b, e := os.ReadFile(w.target()); e == nil {
		result["physicalTranscriptUTF8"] = string(b)
	} else if !os.IsNotExist(e) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { result["trackedAtObservation"] = p.tracked; return nil })
	destination := os.Getenv("CC_HAHA_RECORD_NATIVE_OUTPUT")
	if filepath.Dir(destination) != filepath.Join(home, ".fixture") {
		t.Fatal("private output path required")
	}
	b, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(destination, b, 0600); e != nil {
		t.Fatal(e)
	}
}
