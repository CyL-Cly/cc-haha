package providerproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const GrokDefaultMainModel = "grok-4.7"
const GrokDefaultContextWindow = 500000

type GrokModel struct {
	Raw                     json.RawMessage `json:"-"`
	Value                   string          `json:"value"`
	Label                   string          `json:"label"`
	Description             string          `json:"description"`
	ContextWindow           float64         `json:"contextWindow,omitempty"`
	Source                  string          `json:"source,omitempty"`
	SupportsReasoningEffort *bool           `json:"supportsReasoningEffort,omitempty"`
	ReasoningEffort         string          `json:"reasoningEffort,omitempty"`
	ReasoningEfforts        []string        `json:"reasoningEfforts,omitempty"`
}

func (m GrokModel) MarshalJSON() ([]byte, error) {
	if len(m.Raw) > 0 {
		return append([]byte{}, m.Raw...), nil
	}
	type plain GrokModel
	return json.Marshal(plain(m))
}
func (m *GrokModel) UnmarshalJSON(raw []byte) error {
	type plain GrokModel
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*m = GrokModel(decoded)
	m.Raw = append([]byte{}, protocol.StringifyValidatedJavaScriptJSON(raw)...)
	return nil
}
func GrokModelCatalog() []GrokModel {
	yes := true
	models := []GrokModel{
		{Value: "grok-4.7", Label: "Grok 4.7", Description: "SpaceXAI's latest frontier model"},
		{Value: "grok-4.7-build-fast", Label: "Grok 4.7 Fast", Description: "Fast variant. 2x the price."},
		{Value: "grok-4.6", Label: "Grok 4.6", Description: "Grok 4.6 frontier model"},
		{Value: "grok-4.5", Label: "Grok 4.5", Description: "Grok frontier text model"},
	}
	for i := range models {
		models[i].ContextWindow = GrokDefaultContextWindow
		models[i].Source = "official"
		models[i].SupportsReasoningEffort = &yes
		models[i].ReasoningEffort = "high"
		models[i].ReasoningEfforts = []string{"xhigh", "high", "medium", "low"}
		if i == 3 {
			models[i].ReasoningEfforts = models[i].ReasoningEfforts[1:]
		}
	}
	return models
}
func CloneGrokModels(models []GrokModel) []GrokModel {
	result := append([]GrokModel{}, models...)
	for i := range result {
		result[i].Raw = append([]byte{}, result[i].Raw...)
		result[i].ReasoningEfforts = append([]string{}, result[i].ReasoningEfforts...)
		if result[i].SupportsReasoningEffort != nil {
			v := *result[i].SupportsReasoningEffort
			result[i].SupportsReasoningEffort = &v
		}
	}
	return result
}
func trimGrokString(raw json.RawMessage) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	if len(units) == 0 {
		return nil
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func ResolveGrokModelRaw(raw json.RawMessage) json.RawMessage {
	trimmed := trimGrokString(raw)
	lower := normalizedOpenAIModelKey(raw, true)
	for _, model := range GrokModelCatalog() {
		if bytes.Equal(lower, []byte(`"`+model.Value+`"`)) {
			return lower
		}
	}
	var key string
	_ = json.Unmarshal(lower, &key)
	if len(trimmed) == 0 || strings.HasPrefix(key, "claude-") || key == "default" || key == "grok" || key == "haiku" || key == "sonnet" || key == "opus" {
		return json.RawMessage(`"grok-4.7"`)
	}
	return trimmed
}
func ResolveGrokModel(value string) string {
	raw, _ := json.Marshal(value)
	var result string
	_ = json.Unmarshal(ResolveGrokModelRaw(raw), &result)
	return result
}
func GrokModelCatalogEntry(model string, catalog []GrokModel) *GrokModel {
	raw, _ := json.Marshal(ResolveGrokModel(model))
	for _, entry := range catalog {
		value, _ := json.Marshal(entry.Value)
		if len(entry.Raw) > 0 {
			var obj protocol.JSONObject
			_ = obj.UnmarshalJSON(entry.Raw)
			value = obj.Get("value")
		}
		if bytes.Equal(value, raw) {
			copy := CloneGrokModels([]GrokModel{entry})[0]
			return &copy
		}
	}
	return nil
}
func ResolveGrokReasoningEffort(model string, requested json.RawMessage, catalog []GrokModel) json.RawMessage {
	entry := GrokModelCatalogEntry(model, catalog)
	if entry == nil {
		entry = GrokModelCatalogEntry(model, GrokModelCatalog())
	}
	var effort string
	stringRequested := len(requested) > 0 && requested[0] == '"' && json.Unmarshal(requested, &effort) == nil
	if entry == nil {
		if stringRequested {
			return requested
		}
		return nil
	}
	if entry.SupportsReasoningEffort != nil && !*entry.SupportsReasoningEffort {
		return nil
	}
	for _, supported := range entry.ReasoningEfforts {
		if stringRequested && supported == effort {
			return requested
		}
	}
	if len(entry.Raw) > 0 {
		var obj protocol.JSONObject
		_ = obj.UnmarshalJSON(entry.Raw)
		return obj.Get("reasoningEffort")
	}
	if entry.ReasoningEffort != "" {
		raw, _ := json.Marshal(entry.ReasoningEffort)
		return raw
	}
	return nil
}

func grokObject(raw json.RawMessage) protocol.JSONObject {
	var obj protocol.JSONObject
	_ = obj.UnmarshalJSON(raw)
	if !obj.IsObject() {
		return protocol.NewJSONObject()
	}
	return obj
}
func firstGrokString(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if trimmed := trimGrokString(value); len(trimmed) > 0 {
			return trimmed
		}
	}
	return nil
}
func normalizeRemoteGrokModel(raw json.RawMessage) *GrokModel {
	obj := grokObject(raw)
	meta, info := grokObject(obj.Get("meta")), grokObject(obj.Get("info"))
	obj.Assign(meta)
	obj.Assign(info)
	if string(obj.Get("hidden")) == "true" || string(obj.Get("supported_in_api")) == "false" || string(obj.Get("supportedInApi")) == "false" {
		return nil
	}
	id := firstGrokString(obj.Get("id"), obj.Get("slug"), obj.Get("model"), obj.Get("value"), obj.Get("__key"))
	if id == nil {
		return nil
	}
	var fallback *GrokModel
	for _, entry := range GrokModelCatalog() {
		if bytes.Equal(id, []byte(`"`+entry.Value+`"`)) {
			copy := entry
			fallback = &copy
			break
		}
	}
	out := protocol.NewJSONObject()
	out.Set("value", id)
	label := firstGrokString(obj.Get("display_name"), obj.Get("displayName"), obj.Get("name"), obj.Get("label"))
	if label == nil {
		label = id
		if fallback != nil {
			label, _ = json.Marshal(fallback.Label)
		}
	}
	out.Set("label", label)
	description := firstGrokString(obj.Get("description"))
	if description == nil {
		description = json.RawMessage(`""`)
		if fallback != nil {
			description, _ = json.Marshal(fallback.Description)
		}
	}
	out.Set("description", description)
	for _, key := range []string{"totalContextTokens", "total_context_tokens", "context_window", "contextWindow"} {
		raw := obj.Get(key)
		var n float64
		if len(raw) > 0 && raw[0] != '"' && string(raw) != "null" && json.Unmarshal(raw, &n) == nil && n > 0 && !math.IsInf(n, 0) {
			out.Set("contextWindow", raw)
			break
		}
	}
	if out.Get("contextWindow") == nil && fallback != nil {
		raw, _ := json.Marshal(fallback.ContextWindow)
		out.Set("contextWindow", raw)
	}
	out.Set("source", json.RawMessage(`"official"`))
	for _, key := range []string{"supportsReasoningEffort", "supports_reasoning_effort"} {
		raw := obj.Get(key)
		if string(raw) == "true" || string(raw) == "false" {
			out.Set("supportsReasoningEffort", raw)
			break
		}
	}
	if effort := firstGrokString(obj.Get("reasoningEffort"), obj.Get("reasoning_effort")); effort != nil {
		out.Set("reasoningEffort", effort)
	}
	for _, key := range []string{"reasoningEfforts", "reasoning_efforts"} {
		var values []json.RawMessage
		if json.Unmarshal(obj.Get(key), &values) != nil {
			continue
		}
		strings := []json.RawMessage{}
		for _, value := range values {
			candidate := firstGrokString(value)
			if candidate == nil {
				item := grokObject(value)
				candidate = firstGrokString(item.Get("value"), item.Get("id"))
			}
			if candidate != nil {
				strings = append(strings, candidate)
			}
		}
		if len(strings) > 0 {
			raw, _ := json.Marshal(strings)
			out.Set("reasoningEfforts", raw)
			break
		}
	}
	encoded, _ := out.MarshalJSON()
	var result GrokModel
	if json.Unmarshal(encoded, &result) != nil {
		return nil
	}
	return &result
}

// NormalizeGrokModelCatalog follows the authenticated feed's arrays and keyed
// maps. JSON property order and unpaired UTF-16 survive through each record.
func NormalizeGrokModelCatalog(raw json.RawMessage) []GrokModel {
	var rows []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &rows)
	} else {
		obj := grokObject(raw)
		selected := raw
		for _, key := range []string{"models", "data"} {
			value := obj.Get(key)
			if len(value) > 0 && (value[0] == '[' || value[0] == '{') {
				selected = value
				break
			}
		}
		if len(selected) > 0 && selected[0] == '[' {
			_ = json.Unmarshal(selected, &rows)
		} else {
			keyed := grokObject(selected)
			for _, field := range keyed.OwnProperties() {
				value := grokObject(field.Value)
				if !value.IsObject() || len(field.Value) == 0 || field.Value[0] != '{' {
					continue
				}
				value.Set("__key", field.NameJSON)
				row, _ := value.MarshalJSON()
				rows = append(rows, row)
			}
		}
	}
	result := []GrokModel{}
	for _, row := range rows {
		if model := normalizeRemoteGrokModel(row); model != nil {
			result = append(result, *model)
		}
	}
	return result
}
func mustGrokJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
