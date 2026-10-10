package sdkhost

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// Actual StructuredIO parser→shared Record→physical writer. This exercises the
// existing input representation, without opening ASK/tool/admission capabilities.
func TestPlainRawIngressSharedNestedRecordReferences(t *testing.T) {
	h, loader, path := cleanupHost(t, newCleanupFiles())
	cleanupCall(t, h.options.Owner, func(scope *cooperative.Scope) error {
		raw, e := chunkIterator(h.options.Owner, []string{`{"type":"user","uuid":"raw-ingress","isVirtual":true,"unknown":{"keep":true},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"write","content":"before","unknown":{"extra":1}}]}}`})
		if e != nil {
			return e
		}
		input, e := NewPlainStructuredInput(h.options.Owner, raw)
		if e != nil {
			return e
		}
		next, e := input.Next(scope)
		if e != nil {
			return e
		}
		parsed, e := next.Await(scope)
		if e != nil {
			return e
		}
		if parsed.Done || !parsed.Defined {
			return fmt.Errorf("raw public parser did not yield original message")
		}
		record := parsed.Value
		future, e := h.pending.BeginRecordLive(scope, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &record}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); e != nil {
			return e
		}
		api, ok := record.Object("message")
		if !ok {
			return fmt.Errorf("raw original API object absent")
		}
		array, ok := api.Array("content")
		if !ok {
			return fmt.Errorf("raw original array absent")
		}
		block, ok := array.Object(0)
		if !ok {
			return fmt.Errorf("raw original tool result absent")
		}
		block.Set("content", json.RawMessage(`"actual original result changed before native drain"`))
		record.Set("lateOuter", json.RawMessage(`true`))
		flush, e := h.pending.Flush(scope)
		if e != nil {
			return e
		}
		return flush.Await(scope)
	})
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data := string(b)
	if !strings.Contains(data, `"content":"actual original result changed before native drain"`) || !strings.Contains(data, `"unknown":{"extra":1}`) || strings.Contains(data, "lateOuter") || strings.Contains(data, "isVirtual") {
		t.Fatal(data)
	}
}
