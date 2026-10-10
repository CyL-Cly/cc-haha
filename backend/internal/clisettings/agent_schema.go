package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
)

// Source SettingsSchema: per-field catches preserve valid siblings and future
// per-agent fields. A malformed record is dropped, not a whole-file failure.
func parseAgentSettings(input protocol.JSONObject, out *protocol.JSONObject) {
	if raw := input.Get("builtInAgentOverrides"); raw != nil && receivedType(raw) == "object" {
		var entries protocol.JSONObject
		_ = entries.UnmarshalJSON(raw)
		result := protocol.NewJSONObject()
		for _, property := range entries.OwnProperties() {
			next := protocol.NewJSONObject()
			if receivedType(property.Value) == "object" {
				var entry protocol.JSONObject
				_ = entry.UnmarshalJSON(property.Value)
				if receivedType(entry.Get("model")) == "string" {
					model := trimAgentModelJSON(entry.Get("model"))
					if string(model) != `""` {
						next.Set("model", model)
					}
				}
				if effort := entry.Get("effort"); validAgentSchemaEffort(effort) {
					next.Set("effort", effort)
				}
				for _, p := range entry.OwnProperties() {
					if string(p.NameJSON) != `"model"` && string(p.NameJSON) != `"effort"` {
						_ = next.SetPropertyJSON(p.NameJSON, p.Value)
					}
				}
			}
			b, _ := next.MarshalJSON()
			_ = result.SetPropertyJSON(property.NameJSON, b)
		}
		b, _ := result.MarshalJSON()
		out.Set("builtInAgentOverrides", b)
	}
	if raw := input.Get("strictPluginOnlyCustomization"); raw != nil {
		switch receivedType(raw) {
		case "boolean":
			out.Set("strictPluginOnlyCustomization", raw)
		case "array":
			var values []json.RawMessage
			_ = json.Unmarshal(raw, &values)
			accepted := []json.RawMessage{}
			for _, value := range values {
				var name string
				if json.Unmarshal(value, &name) == nil && (name == "skills" || name == "agents" || name == "hooks" || name == "mcp") {
					accepted = append(accepted, value)
				}
			}
			b, _ := json.Marshal(accepted)
			out.Set("strictPluginOnlyCustomization", b)
		}
	}
}
func validAgentSchemaEffort(raw json.RawMessage) bool {
	if receivedType(raw) == "string" {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s == "low" || s == "medium" || s == "high" || s == "xhigh" || s == "max"
	}
	var n float64
	return receivedType(raw) == "number" && json.Unmarshal(raw, &n) == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
}
func processedAgentProperty(key json.RawMessage) bool {
	return string(key) == `"builtInAgentOverrides"` || string(key) == `"strictPluginOnlyCustomization"`
}

// Trim UTF16 units directly so Source-representable lone units survive the shared
// schema. The native listing projection has its own explicit unsupported guard.
func trimAgentModelJSON(raw json.RawMessage) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	space := func(u uint16) bool { return trimJS(string(rune(u))) == "" }
	for len(units) > 0 && space(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && space(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
