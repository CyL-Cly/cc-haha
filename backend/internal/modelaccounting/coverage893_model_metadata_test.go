package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestCoverage893PersistedModelMetadataKeepsExplicitWindowAndCanonicalPolicies(t *testing.T) {
	for _, tc := range []struct {
		model, disabled string
		window, output  float64
	}{
		{"private-model[1m]", "", 1000000, 32000}, {"private-model:1m", "", 1000000, 32000}, {"private-model[1m]", "true", 200000, 32000},
		{"anthropic/claude-opus-4.8-release", "", 1000000, 32000}, {"openai/gpt-4.1-private", "", 1047576, 32000}, {"google/gemini-3-private", "", 1048576, 32000}, {"relay-claude-private[extra]", "", 200000, 32000},
	} {
		t.Run(tc.model+tc.disabled, func(t *testing.T) {
			r := RestoredModelResolver{Environment: map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": tc.disabled}}
			w, out, e := r.Resolve(tc.model)
			if e != nil || w != tc.window || out != tc.output {
				t.Fatal(w, out, e)
			}
		})
	}
	r := RestoredModelResolver{Settings: func() (*protocol.JSONObject, error) {
		return storedProject(t, `{"modelOverrides":{"claude-sonnet-5-5":"provider/private-name"},"future":{"preserved":true}}`), nil
	}}
	canonical, e := r.Canonical("provider/private-name")
	if e != nil || canonical != "claude-sonnet-5-5" {
		t.Fatal(canonical, e)
	}
	_, out, e := r.Resolve("provider/private-name")
	if e != nil || out != 128000 {
		t.Fatal(out, e)
	}
}
