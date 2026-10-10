package toolsearch

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type GateInputs struct {
	Environment         map[string]string
	UnsupportedPatterns func() []string
	// This is the original countToolDefinitionTokens consumer. nil is a
	// missing producer, not a fabricated token-API failure.
	CountDefinitionTokens func(context.Context, []Tool, string) (*float64, error)
	// Same-owner consumers must suspend cooperatively while another query's
	// memoized count promise is pending. A missing consumer is an explicit cap.
	AwaitTokenCount func(context.Context, <-chan struct{}) error
	ContextWindow   func(context.Context, string) (float64, error)
	DeltaEnabled    func() bool
}
type RequestPlan struct {
	Enabled  bool
	Included []int
	Deferred []json.RawMessage
	Beta     string
	Delta    bool
}

func envTruthy(s string) bool {
	switch strings.ToLower(string(utf16.Decode(trim(utf16.Encode([]rune(s)))))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func envFalsy(s string) bool {
	switch strings.ToLower(string(utf16.Decode(trim(utf16.Encode([]rune(s)))))) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func autoPercentage(value string) (float64, bool) {
	if !strings.HasPrefix(value, "auto:") {
		return 0, false
	}
	text := string(utf16.Decode(trim(utf16.Encode([]rune(value[5:])))))
	sign := 1.0
	if strings.HasPrefix(text, "-") {
		sign = -1
		text = text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, e := strconv.ParseFloat(text[:end], 64)
	if e != nil && !math.IsInf(n, 0) {
		return 0, false
	}
	return math.Max(0, math.Min(100, n*sign)), true
}
func Mode(env map[string]string) string {
	if envTruthy(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		return "standard"
	}
	value := env["ENABLE_TOOL_SEARCH"]
	if percent, ok := autoPercentage(value); ok {
		if percent == 0 {
			return "tst"
		}
		if percent == 100 {
			return "standard"
		}
	}
	if value == "auto" || strings.HasPrefix(value, "auto:") {
		return "tst-auto"
	}
	if envFalsy(value) {
		return "standard"
	}
	return "tst"
}
func Provider(env map[string]string) string {
	for _, row := range [][2]string{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTruthy(env[row[0]]) {
			return row[1]
		}
	}
	return "firstParty"
}
func Optimistic(env map[string]string) bool {
	if Mode(env) == "standard" {
		return false
	}
	if env["ENABLE_TOOL_SEARCH"] != "" || Provider(env) != "firstParty" {
		return true
	}
	base := env["ANTHROPIC_BASE_URL"]
	if base == "" {
		return true
	}
	parsed, e := weburl.Parse(base)
	if e != nil {
		return false
	}
	return parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com"
}
func (s *Service) SupportsModel(model string, inputs GateInputs) (bool, error) {
	patterns := []string{"haiku"}
	if inputs.UnsupportedPatterns != nil {
		if supplied := inputs.UnsupportedPatterns(); len(supplied) > 0 {
			patterns = supplied
		}
	}
	name, e := s.lower(utf16.Encode([]rune(model)))
	if e != nil {
		return false, e
	}
	for _, pattern := range patterns {
		p, e := s.lower(utf16.Encode([]rune(pattern)))
		if e != nil {
			return false, e
		}
		if has(name, p) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) Plan(ctx context.Context, model string, tools []Tool, history []protocol.JSONObject, pending bool, inputs GateInputs) (RequestPlan, error) {
	plan := RequestPlan{Included: []int{}, Deferred: []json.RawMessage{}}
	support, e := s.SupportsModel(model, inputs)
	if e != nil {
		return plan, e
	}
	enabled := support && find(tools, utf16.Encode([]rune(Name))) != nil
	mode := Mode(inputs.Environment)
	if mode == "standard" {
		enabled = false
	}
	deferred := []Tool{}
	for _, t := range tools {
		if IsDeferred(t) {
			deferred = append(deferred, t)
			plan.Deferred = append(plan.Deferred, t.Name)
		}
	}
	if enabled && mode == "tst-auto" {
		percentage := 10.0
		if n, ok := autoPercentage(inputs.Environment["ENABLE_TOOL_SEARCH"]); ok {
			percentage = n
		}
		if len(deferred) > 0 {
			count, e := s.deferredTokenCount(ctx, deferred, model, inputs)
			if e != nil {
				return plan, e
			}
			if inputs.ContextWindow == nil {
				return plan, &CapabilityError{"original model context-window producer"}
			}
			window, e := inputs.ContextWindow(ctx, model)
			if e != nil {
				return plan, e
			}
			threshold := math.Floor(window * percentage / 100)
			if count != nil {
				enabled = *count >= threshold
			} else {
				characters := 0
				for _, tool := range deferred {
					if tool.Prompt == nil || tool.Schema == nil {
						return plan, &CapabilityError{"original deferred prompt/schema char fallback"}
					}
					p, e := tool.Prompt(ctx)
					if e != nil {
						return plan, e
					}
					schema, e := tool.Schema(ctx)
					if e != nil {
						return plan, e
					}
					serialized, e := protocol.ParseAndStringifyJavaScriptJSON(schema)
					if e != nil {
						return plan, e
					}
					characters += len(units(tool.Name)) + len(units(p)) + len(utf16.Encode([]rune(string(serialized))))
				}
				enabled = float64(characters) >= math.Floor(threshold*2.5)
			}
		} else {
			if inputs.ContextWindow == nil {
				return plan, &CapabilityError{"original model context-window producer"}
			}
			window, e := inputs.ContextWindow(ctx, model)
			if e != nil {
				return plan, e
			}
			enabled = 0 >= math.Floor(window*percentage/100)
		}
	}
	if enabled && len(deferred) == 0 && !pending {
		enabled = false
	}
	plan.Enabled = enabled
	discovered := Discovered(history)
	for i, t := range tools {
		if enabled {
			if !IsDeferred(t) || discovered[string(protocol.StringifyValidatedJavaScriptJSON(t.Name))] {
				plan.Included = append(plan.Included, i)
			}
		} else if !same(t.Name, quoted(Name)) {
			plan.Included = append(plan.Included, i)
		}
	}
	if enabled {
		plan.Beta = "advanced-tool-use-2025-11-20"
		if p := Provider(inputs.Environment); p == "vertex" || p == "bedrock" {
			plan.Beta = "tool-search-tool-2025-10-19"
		}
		plan.Delta = inputs.Environment["USER_TYPE"] == "ant"
		if inputs.DeltaEnabled != nil {
			plan.Delta = plan.Delta || inputs.DeltaEnabled()
		}
	}
	return plan, nil
}

func Discovered(history []protocol.JSONObject) map[string]bool {
	out := map[string]bool{}
	add := func(name json.RawMessage) {
		if _, e := protocol.DecodeJavaScriptJSONString(name); e == nil {
			out[string(protocol.StringifyValidatedJavaScriptJSON(name))] = true
		}
	}
	for _, record := range history {
		if record.String("type") == "system" && record.String("subtype") == "compact_boundary" {
			meta, _ := record.Object("compactMetadata")
			var names []json.RawMessage
			_ = json.Unmarshal(meta.Get("preCompactDiscoveredTools"), &names)
			for _, name := range names {
				add(name)
			}
			continue
		}
		if record.String("type") != "user" {
			continue
		}
		message, _ := record.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") != "tool_result" {
				continue
			}
			var parts []protocol.JSONObject
			_ = json.Unmarshal(block.Get("content"), &parts)
			for _, part := range parts {
				if part.String("type") == "tool_reference" {
					add(part.Get("tool_name"))
				}
			}
		}
	}
	return out
}

func Prompt(delta bool) string {
	hint := "Deferred tools appear by name in <available-deferred-tools> messages."
	if delta {
		hint = "Deferred tools appear by name in <system-reminder> messages."
	}
	return "Fetches full schema definitions for deferred tools so they can be called.\n\n" + hint + " Until fetched, only the name is known — there is no parameter schema, so the tool cannot be invoked. This tool takes a query, matches it against the deferred tool list, and returns the matched tools' complete JSONSchema definitions inside a <functions> block. Once a tool's schema appears in that result, it is callable exactly like any tool defined at the top of the prompt.\n\nResult format: each matched tool appears as one <function>{\"description\": \"...\", \"name\": \"...\", \"parameters\": {...}}</function> line inside the <functions> block — the same encoding as the tool list at the top of this prompt.\n\nQuery forms:\n- \"select:Read,Edit,Grep\" — fetch these exact tools by name\n- \"notebook jupyter\" — keyword search, up to max_results best matches\n- \"+slack send\" — require \"slack\" in the name, rank by remaining terms"
}
