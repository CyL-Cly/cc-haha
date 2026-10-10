package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestContextWindowGetterDoesNotInvokeUnrelatedOutputCanonicalGetter(t *testing.T) {
	reads := 0
	r := RestoredModelResolver{Environment: map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"private-model":64000}`}, Settings: func() (*protocol.JSONObject, error) { reads++; p := protocol.NewJSONObject(); return &p, nil }}
	window, err := r.ContextWindow("private-model[1m]")
	if err != nil || window != 64000 || reads != 0 {
		t.Fatal("context-only getter changed Source producer order", window, reads, err)
	}
	r.Environment = nil
	r.SDKBetas = []string{"context-1m-2025-08-07"}
	r.Settings = func() (*protocol.JSONObject, error) {
		reads++
		p := protocol.NewJSONObject(protocol.JSONField{Name: "modelOverrides", Value: []byte(`{"claude-sonnet-4-6":"private-model"}`)})
		return &p, nil
	}
	window, err = r.ContextWindow("private-model")
	if err != nil || window != 1000000 || reads != 1 {
		t.Fatal("actual beta/canonical context resolution changed", window, reads, err)
	}
}
