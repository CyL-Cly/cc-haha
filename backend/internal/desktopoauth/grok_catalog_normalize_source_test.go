package desktopoauth

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func TestGrokRemoteCatalogNormalizationAndResolutionMatchesActualSource(t *testing.T) {
	rawBodies := []string{
		`[{"id":"grok-4.7"}]`,
		`{"models":{"first":{"description":"  hello  ","info":{"displayName":" Remote ","context_window":1234.8,"reasoning_efforts":["high"," ",{"value":" max "},{"id":" low "},false],"supports_reasoning_effort":true}},"second":{"slug":" next ","meta":{"slug":" outer "},"info":{"slug":" inner "}},"array":[],"primitive":3}}`,
		`[{"id":"outer","meta":{"id":"meta","info":{"id":"must-not-win"}},"info":{"id":"actual-info","meta":{"id":"also-not-used"}}}]`,
		`{"data":[{"id":"hidden","hidden":true},{"id":"no","supported_in_api":false},{"id":"no","supportedInApi":false},{"id":"valid","hidden":"true","supportsReasoningEffort":false,"reasoningEffort":" high "}]}`,
		`{"7":{"id":" ","slug":"slug"},"2":{"model":"model"},"x":{"value":"value"},"fallback":{}}`,
		`[{"id":"\ud800","display_name":"  lone\udfff  ","description":"  \ud800  ","totalContextTokens":1e308,"reasoningEfforts":["\ud800"]},{"id":"\ufffd"}]`,
		`[{"id":"grok-4.5","contextWindow":0},{"id":"grok-4.7","contextWindow":"123"},{"id":"future","reasoningEfforts":[],"reasoning_efforts":["low"],"supportsReasoningEffort":null,"supports_reasoning_effort":true}]`,
		`null`, `false`, `[]`, `{"models":[]}`, `{"models":{},"data":[{"id":"must-ignore"}]}`,
	}
	rows := []map[string]any{}
	for _, body := range rawBodies {
		rows = append(rows, map[string]any{"body": body})
	}
	lookups := []map[string]any{}
	for _, model := range []string{" GROK-4.7 ", "default", "claude-opus-5-5", "Haiku", " new-remote ", "\u00a0grok\ufeff"} {
		for _, effort := range []any{"low", "max", "", "high", nil, 42} {
			lookups = append(lookups, map[string]any{"model": model, "effort": effort})
		}
	}
	lookups = append(lookups, map[string]any{"model": "future", "effort": "ultra", "catalog": []any{map[string]any{"value": "future", "supportsReasoningEffort": false, "reasoningEffort": "high", "reasoningEfforts": []string{"ultra"}}}})
	var source struct{ Observations, Lookups []json.RawMessage }
	if err := json.Unmarshal(actualGrokCatalogSource(t, map[string]any{"rows": rows, "lookups": lookups}), &source); err != nil {
		t.Fatal(err)
	}
	for i, body := range rawBodies {
		var want struct {
			Value json.RawMessage
			Error string
		}
		_ = json.Unmarshal(source.Observations[i], &want)
		models := providerproxy.NormalizeGrokModelCatalog(json.RawMessage(body))
		if want.Error != "" {
			if len(models) != 0 {
				t.Errorf("row%d models despite original error %s", i, want.Error)
			}
			continue
		}
		if !bytes.Equal(encode(models), want.Value) {
			t.Errorf("row%d got%s want%s", i, encode(models), want.Value)
		}
	}
	for i, row := range lookups {
		catalog := providerproxy.GrokModelCatalog()
		if value, present := row["catalog"]; present {
			_ = json.Unmarshal(encode(value), &catalog)
		}
		effort := providerproxy.ResolveGrokReasoningEffort(row["model"].(string), encode(row["effort"]), catalog)
		if effort == nil {
			effort = json.RawMessage("null")
		}
		got := map[string]any{"model": providerproxy.ResolveGrokModel(row["model"].(string)), "effort": effort}
		var actual, want any
		_ = json.Unmarshal(encode(got), &actual)
		_ = json.Unmarshal(source.Lookups[i], &want)
		if !bytes.Equal(encode(actual), encode(want)) {
			t.Errorf("lookup%d got%s want%s", i, encode(actual), encode(want))
		}
	}
}
