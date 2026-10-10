package providerproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const OpenAIDefaultMainModel = "gpt-6-sol"

type OpenAIModel struct {
	// Remote discovery retains the original JSON string units and number
	// representation; the convenience fields remain available to consumers.
	Raw                       json.RawMessage `json:"-"`
	Value                     string          `json:"value"`
	Label                     string          `json:"label"`
	Description               string          `json:"description"`
	DescriptionForModel       string          `json:"descriptionForModel,omitempty"`
	DefaultReasoningEffort    string          `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []string        `json:"supportedReasoningEfforts"`
	ContextWindow             int             `json:"contextWindow,omitempty"`
}

func (model OpenAIModel) MarshalJSON() ([]byte, error) {
	if len(model.Raw) != 0 {
		return append([]byte{}, model.Raw...), nil
	}
	type plain OpenAIModel
	return json.Marshal(plain(model))
}

func (model *OpenAIModel) UnmarshalJSON(raw []byte) error {
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(raw); err != nil {
		return err
	}
	contextRaw := object.Get("contextWindow")
	object.Delete("contextWindow")
	type plain OpenAIModel
	var decoded plain
	if err := json.Unmarshal(protocol.StringifyValidatedJavaScriptJSON(mustModelJSON(object)), &decoded); err != nil {
		return err
	}
	var n float64
	if json.Unmarshal(contextRaw, &n) == nil && n >= float64(math.MinInt) && n < float64(math.MaxInt) {
		decoded.ContextWindow = int(n)
	}
	*model = OpenAIModel(decoded)
	model.Raw = append([]byte{}, protocol.StringifyValidatedJavaScriptJSON(raw)...)
	return nil
}

func mustModelJSON(object protocol.JSONObject) []byte { raw, _ := object.MarshalJSON(); return raw }

func normalizedOpenAIModelKey(raw json.RawMessage, trim bool) []byte {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	for trim && len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for trim && len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	result := make([]uint16, 0, len(units))
	lower := cases.Lower(language.Und)
	for start := 0; start < len(units); {
		end := start
		for end < len(units) {
			unit := units[end]
			if unit >= 0xd800 && unit <= 0xdbff && end+1 < len(units) && units[end+1] >= 0xdc00 && units[end+1] <= 0xdfff {
				end += 2
				continue
			}
			if unit >= 0xd800 && unit <= 0xdfff {
				break
			}
			end++
		}
		result = append(result, utf16.Encode([]rune(lower.String(string(utf16.Decode(units[start:end])))))...)
		if end < len(units) {
			result = append(result, units[end])
			end++
		}
		start = end
	}
	return protocol.QuoteJavaScriptUTF16(result)
}

// OpenAIModelCatalog is the bundled OAuth catalog, distinct from the public
// API model limits. Returning independent slices preserves its read boundary.
func OpenAIModelCatalog() []OpenAIModel {
	models := []OpenAIModel{
		{Value: "gpt-6-astra", Label: "GPT-6 Astra", Description: "Frontier model for complex reasoning and agentic work", DescriptionForModel: "GPT-6 Astra - complex reasoning and agentic work", DefaultReasoningEffort: "low", ContextWindow: 258400},
		{Value: "gpt-6-sol", Label: "GPT-6-Sol", Description: "Workhorse model for coding and everyday work", DescriptionForModel: "GPT-6-Sol - workhorse model for coding and everyday work", DefaultReasoningEffort: "medium", ContextWindow: 258400},
		{Value: "gpt-6-luna", Label: "GPT-6-Luna", Description: "Fast and affordable model for easier tasks", DescriptionForModel: "GPT-6-Luna - fast and affordable model for easier tasks", DefaultReasoningEffort: "medium", ContextWindow: 258400},
		{Value: "gpt-6.1-sol", Label: "GPT-6.1-Sol", Description: "Latest workhorse model for coding and everyday work", DescriptionForModel: "GPT-6.1-Sol - latest workhorse model for coding and everyday work", DefaultReasoningEffort: "low", ContextWindow: 258400},
		{Value: "gpt-5.6-sol", Label: "GPT-5.6-Sol", Description: "Frontier agentic coding model", DescriptionForModel: "GPT-5.6-Sol - frontier agentic coding model", DefaultReasoningEffort: "low", ContextWindow: 353400},
		{Value: "gpt-5.6-terra", Label: "GPT-5.6-Terra", Description: "Balanced agentic coding model for everyday work", DescriptionForModel: "GPT-5.6-Terra - balanced agentic coding model", DefaultReasoningEffort: "medium", ContextWindow: 353400},
		{Value: "gpt-5.6-luna", Label: "GPT-5.6-Luna", Description: "Fast and affordable agentic coding model", DescriptionForModel: "GPT-5.6-Luna - fast and affordable agentic coding model", DefaultReasoningEffort: "medium", ContextWindow: 353400},
		{Value: "gpt-5.3-codex", Label: "GPT-5.3 Codex", Description: "Best for coding and agentic work", DescriptionForModel: "GPT-5.3 Codex - best for coding and agentic work", DefaultReasoningEffort: "medium", ContextWindow: 258400},
		{Value: "gpt-5.4", Label: "GPT-5.4", Description: "Strong general-purpose model", DescriptionForModel: "GPT-5.4 - strong general-purpose model", DefaultReasoningEffort: "medium", ContextWindow: 950000},
		{Value: "gpt-5.5", Label: "GPT-5.5", Description: "Latest general-purpose model", DescriptionForModel: "GPT-5.5 - latest general-purpose model", DefaultReasoningEffort: "medium", ContextWindow: 258400},
		{Value: "gpt-5.4-mini", Label: "GPT-5.4 Mini", Description: "Fastest for quick tasks", DescriptionForModel: "GPT-5.4 Mini - fastest for quick tasks", DefaultReasoningEffort: "medium", ContextWindow: 258400},
	}
	for i := range models {
		models[i].SupportedReasoningEfforts = []string{"low", "medium", "high", "xhigh"}
		if i < 7 {
			models[i].SupportedReasoningEfforts = append(models[i].SupportedReasoningEfforts, "max")
		}
	}
	return models
}

func IsOpenAIReasoningEffort(raw json.RawMessage) bool {
	var effort string
	if json.Unmarshal(raw, &effort) != nil {
		return false
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

func OpenAIModelCatalogEntry(model string, catalog []OpenAIModel) *OpenAIModel {
	modelRaw, _ := json.Marshal(model)
	key := normalizedOpenAIModelKey(modelRaw, true)
	for _, candidate := range catalog {
		value, _ := json.Marshal(candidate.Value)
		if len(candidate.Raw) != 0 {
			var object protocol.JSONObject
			_ = object.UnmarshalJSON(candidate.Raw)
			value = object.Get("value")
		}
		if bytes.Equal(normalizedOpenAIModelKey(value, false), key) {
			copy := candidate
			copy.Raw = append([]byte{}, candidate.Raw...)
			copy.SupportedReasoningEfforts = append([]string{}, candidate.SupportedReasoningEfforts...)
			return &copy
		}
	}
	return nil
}

func ResolveOpenAIReasoningEffort(model string, efforts []json.RawMessage) string {
	entry := OpenAIModelCatalogEntry(model, OpenAIModelCatalog())
	for _, raw := range efforts {
		if !IsOpenAIReasoningEffort(raw) {
			continue
		}
		var effort string
		_ = json.Unmarshal(raw, &effort)
		if entry == nil {
			return effort
		}
		for _, supported := range entry.SupportedReasoningEfforts {
			if supported == effort {
				return effort
			}
		}
	}
	if entry != nil {
		return entry.DefaultReasoningEffort
	}
	return "medium"
}

func IsOpenAIResponsesModel(model string) bool {
	key := strings.ToLower(protocol.TrimJavaScriptString(model))
	// The shared Source normalizer removes both suffixes in this exact order.
	key = strings.TrimSuffix(strings.TrimSuffix(key, "[1m]"), ":1m")
	if index := strings.LastIndexByte(key, '/'); index >= 0 {
		key = key[index+1:]
	}
	return strings.HasPrefix(key, "gpt-") || len(key) > 1 && key[0] == 'o' && key[1] >= '0' && key[1] <= '9'
}

func ResolveOpenAICodexModel(model string, env map[string]string) string {
	if override := protocol.TrimJavaScriptString(env["OPENAI_CODEX_MODEL"]); override != "" {
		return override
	}
	key := strings.ToLower(protocol.TrimJavaScriptString(model))
	if IsOpenAIResponsesModel(key) {
		return model
	}
	for _, slot := range []struct{ name, fallback string }{{"haiku", "gpt-6-luna"}, {"sonnet", "gpt-6-sol"}, {"opus", "gpt-6-astra"}} {
		if strings.Contains(key, slot.name) {
			if override := protocol.TrimJavaScriptString(env["OPENAI_CODEX_"+strings.ToUpper(slot.name)+"_MODEL"]); override != "" {
				return override
			}
			return slot.fallback
		}
	}
	return OpenAIDefaultMainModel
}

func OpenAIModelDisplayName(model string) string {
	key := strings.ToLower(protocol.TrimJavaScriptString(model))
	if key == "gpt-6" {
		return "GPT-6 Astra"
	}
	if entry := OpenAIModelCatalogEntry(key, OpenAIModelCatalog()); entry != nil {
		return entry.Label
	}
	switch key {
	case "gpt-5.3-codex-spark":
		return "GPT-5.3 Codex Spark"
	case "gpt-5.2":
		return "GPT-5.2"
	case "gpt-5.2-codex":
		return "GPT-5.2 Codex"
	case "gpt-5.1-codex":
		return "GPT-5.1 Codex"
	case "gpt-5.1-codex-max":
		return "GPT-5.1 Codex Max"
	case "gpt-5.1-codex-mini":
		return "GPT-5.1 Codex Mini"
	}
	return ""
}

func OpenAICodexContextWindow(model string) int {
	switch strings.ToLower(protocol.TrimJavaScriptString(model)) {
	case "gpt-6-astra", "gpt-6", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.5", "gpt-5.5-pro", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex", "gpt-5.2", "gpt-5.2-codex", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini", "gpt-5-codex", "gpt-5", "gpt-5-mini", "gpt-5-nano":
		return 258400
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6":
		return 353400
	case "gpt-5.4", "gpt-5.4-pro":
		return 950000
	case "gpt-5.3-codex-spark":
		return 121600
	}
	return 0
}
