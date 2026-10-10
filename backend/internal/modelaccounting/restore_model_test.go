package modelaccounting

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestRestoreModelMetadataUsesKnownDefaultConfiguredAndAliasProducers(t *testing.T) {
	for _, p := range []struct {
		model          string
		env            map[string]string
		window, output float64
	}{
		{"fixture-model", nil, 200000, 32000}, {"deepseek-flash", nil, 200000, 32000}, {"deepseek-v4-flash", nil, 1000000, 32000},
		{"claude-sonnet-4-6", nil, 200000, 32000}, {"claude-sonnet-5", nil, 1000000, 64000}, {"us.anthropic.claude-opus-4-6-v1:0", nil, 200000, 32000},
		{"gpt-6-sol", nil, 258400, 32000}, {"gpt-5.6-sol", nil, 353400, 32000}, {"gpt-5.4", nil, 950000, 32000}, {"gpt-5.3-codex-spark", nil, 121600, 32000},
		{"unknown[1m]", map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"unknown":32000}`}, 32000, 32000},
		{"vendor:unknown", map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"unknown":64000}`}, 64000, 32000},
		{"deepseek-v4-flash", map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": " on "}, 200000, 32000},
		{"deepseek-v4-flash", map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "arbitrary"}, 1000000, 32000},
		{"unknown", map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"unknown":"40000"}`}, 200000, 32000},
		{"unknown:1m[1m]", map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"unknown":64000}`}, 64000, 32000},
		{"deepseek-v4-flash", map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "\ufeffON\ufeff"}, 200000, 32000},
	} {
		t.Run(p.model, func(t *testing.T) {
			r := RestoredModelResolver{Environment: p.env}
			w, o, e := r.Resolve(p.model)
			if e != nil || w != p.window || o != p.output {
				t.Fatal(w, o, e, p)
			}
		})
	}
	settings := func() (*protocol.JSONObject, error) {
		p := protocol.NewJSONObject(protocol.JSONField{Name: "modelOverrides", Value: []byte(`{"claude-sonnet-5":"private-relay-id"}`)})
		return &p, nil
	}
	r := RestoredModelResolver{Settings: settings}
	w, o, e := r.Resolve("private-relay-id")
	if e != nil || w != 200000 || o != 64000 {
		t.Fatal("context raw ID/default and output canonical override must differ", w, o, e)
	}
	r.SDKBetas = []string{"context-1m-2025-08-07"}
	w, o, e = r.Resolve("private-relay-id")
	if e != nil || w != 1000000 || o != 64000 {
		t.Fatal(w, o, e)
	}
}
func TestRestoreContextThenOutputGetterOrderAndInternalCapability(t *testing.T) {
	var events []string
	r := RestoredModelResolver{Settings: func() (*protocol.JSONObject, error) {
		events = append(events, "settings")
		p := protocol.NewJSONObject()
		return &p, nil
	}, Environment: map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"fixture-model":64000}`}}
	w, o, e := r.Resolve("fixture-model")
	if e != nil || w != 64000 || o != 32000 || len(events) != 1 {
		t.Fatal(events, w, o, e)
	}
	r.Environment = map[string]string{"USER_TYPE": "ant"}
	_, _, e = r.Resolve("fixture-model")
	var cap *CapabilityError
	if !errors.As(e, &cap) {
		t.Fatal("internal cache/capability lookup silently defaulted", e)
	}
}
