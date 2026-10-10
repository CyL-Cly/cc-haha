package querypreflight

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"math"
	"regexp"
	"strings"
)

const NativeProcessProfile = "native-direct-relay-model-budget-v1"

// Literal Source modelContextWindows tables, not captured output or an inferred
// endpoint limit. Provider-specific windows override these original defaults.
var nativeDirectWindows = map[string]float64{
	"claude-fable-5-1":            1000000,
	"claude-fable-5":              1000000,
	"claude-opus-5-5":             1000000,
	"claude-opus-5":               1000000,
	"claude-opus-4-8":             1000000,
	"claude-opus-4-7":             1000000,
	"claude-sonnet-5-5":           1000000,
	"claude-sonnet-5":             1000000,
	"claude-sonnet-4-6":           200000,
	"claude-haiku-4-5":            200000,
	"deepseek-v4-pro":             1000000,
	"deepseek-ai/deepseek-v4-pro": 1000000,
	"deepseek-v4-flash":           1000000,
	"deepseek-chat":               1000000,
	"deepseek-reasoner":           1000000,
	"k3":                          262144,
	"kimi-for-coding":             262144,
	"kimi-for-coding-highspeed":   262144,
	"kimi-k2.7-code":              262144,
	"kimi-k2.7-code-highspeed":    262144,
	"kimi-k2.6":                   262144,
	"kimi-k2.5":                   262144,
	"kimi-k2-0905-preview":        262144,
	"kimi-k2-turbo-preview":       262144,
	"kimi-k2-thinking":            262144,
	"kimi-k2-thinking-turbo":      262144,
	"minimax-m3":                  1000000,
	"minimax-m2.7":                204800,
	"minimax-m2.7-highspeed":      204800,
	"qwen/qwen3.6-27b":            262144,
	"qwen3.6:27b":                 262144,
	"glm-5.3":                     1000000,
	"glm-5.3-flash":               1000000,
	"glm-5.2":                     1000000,
	"glm-5.1":                     200000,
	"glm-5":                       200000,
	"glm-5-turbo":                 200000,
	"glm-4.7":                     200000,
	"glm-4.6":                     200000,
	"glm-4.5":                     128000,
	"glm-4.5-air":                 128000,
}
var nativePatternWindows = []struct {
	pattern *regexp.Regexp
	window  float64
}{
	{regexp.MustCompile(`(?i)^anthropic/claude-opus-5\b`), 1000000},
	{regexp.MustCompile(`(?i)^anthropic/claude-fable-5\b`), 1000000},
	{regexp.MustCompile(`(?i)^anthropic/claude-opus-4\.8\b`), 1000000},
	{regexp.MustCompile(`(?i)^anthropic/claude-sonnet-5\b`), 1000000},
	{regexp.MustCompile(`(?i)^anthropic/claude-opus-4\.7\b`), 1000000},
	{regexp.MustCompile(`(?i)^anthropic/claude-sonnet-4\.6\b`), 200000},
	{regexp.MustCompile(`(?i)^anthropic/claude-haiku-4\.5\b`), 200000},
	{regexp.MustCompile(`(?i)^openai/gpt-4\.1\b`), 1047576},
	{regexp.MustCompile(`(?i)^openai/gpt-5(?:[.-]\d+)?\b`), 400000},
	{regexp.MustCompile(`(?i)^google/gemini-(?:2\.0|2\.5|3)`), 1048576},
	{regexp.MustCompile(`(?i)^gemini-(?:2\.0|2\.5|3)`), 1048576},
	{regexp.MustCompile(`(?i)^zai-org/glm-5\.3(?:-flash)?\b`), 1000000},
	{regexp.MustCompile(`(?i)^zai-org/glm-5\.2\b`), 1000000},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3\.7-(?:max|plus)(?:[-.][\w.-]+)?\b`), 1000000},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3\.6-(?:plus|flash)(?:[-.][\w.-]+)?\b`), 1000000},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3\.6-max-preview\b`), 262144},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3\.5-(?:plus|flash)(?:[-.][\w.-]+)?\b`), 1000000},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3-max(?:[-.][\w.-]+)?\b`), 262144},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3-coder-plus(?:[-.][\w.-]+)?\b`), 1000000},
	{regexp.MustCompile(`(?i)^(?:qwen/)?qwen3-coder-next(?:[-.][\w.-]+)?\b`), 262144},
	{regexp.MustCompile(`(?i)qwen-long`), 10000000},
}
var nativeContextSuffix = regexp.MustCompile(`(?i)\[1m\]$`)

func nativeModel(model string) (string, error) {
	normalized := strings.ToLower(trimmed(model))
	alias := strings.TrimSuffix(normalized, "[1m]")
	switch trimmed(alias) {
	case "opusplan", "fable", "sonnet", "haiku", "opus", "best":
		return "", capError("native main model must be resolved by original startup alias consumer")
	}
	if normalized == "" {
		return "", capError("empty native main model")
	}
	return model, nil
}
func nativeModelKey(model string) string {
	return strings.TrimSuffix(nativeContextSuffix.ReplaceAllString(strings.ToLower(trimmed(model)), ""), ":1m")
}
func nativeConfiguredWindow(model, raw string) (float64, bool) {
	var original protocol.JSONObject
	if json.Unmarshal([]byte(raw), &original) != nil || !original.IsObject() {
		return 0, false
	}
	configured := protocol.NewJSONObject()
	for _, p := range original.OwnProperties() {
		var value float64
		if json.Unmarshal(p.Value, &value) != nil || math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value || value < 16000 || value > 10000000 {
			continue
		}
		var key string
		if json.Unmarshal(p.NameJSON, &key) != nil {
			continue
		}
		configured.Set(nativeModelKey(key), p.Value)
	}
	normalized := nativeModelKey(model)
	if raw := configured.Get(normalized); raw != nil {
		var value float64
		_ = json.Unmarshal(raw, &value)
		return value, true
	}
	for _, p := range configured.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		if strings.HasSuffix(normalized, "/"+key) || strings.HasSuffix(normalized, ":"+key) {
			var value float64
			_ = json.Unmarshal(p.Value, &value)
			return value, true
		}
	}
	return 0, false
}
func nativeHas1m(model string, env map[string]string) bool {
	return !envTruthy(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (strings.Contains(strings.ToLower(model), "[1m]") || strings.HasSuffix(strings.ToLower(model), ":1m"))
}
func nativeBuiltInWindow(model string) (float64, bool) {
	key := nativeModelKey(model)
	if value, ok := nativeDirectWindows[key]; ok {
		return value, true
	}
	for _, entry := range nativePatternWindows {
		if entry.pattern.MatchString(key) {
			return entry.window, true
		}
	}
	return 0, false
}
func nativeContextWindow(model string, env map[string]string) (float64, bool) {
	if value, ok := nativeConfiguredWindow(model, env["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]); ok {
		if value > 200000 && envTruthy(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
			value = 200000
		}
		return value, true
	}
	if nativeHas1m(model, env) {
		return 1000000, true
	}
	if value, ok := nativeBuiltInWindow(model); ok {
		if value > 200000 && envTruthy(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
			value = 200000
		}
		return value, true
	}
	return 200000, false
}
func nativeThresholds(model string, env map[string]string) (float64, float64) {
	window, known := nativeContextWindow(model, env)
	if override := parseInteger(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]); override > 0 {
		if known {
			window = math.Min(window, override)
		} else {
			window = override
		}
	}
	reserve := math.Min(requestcontext.NativeConfiguredMaxOutputTokens(model, env), math.Min(20000, math.Floor(window*.25)))
	effective := window - reserve
	threshold := effective - math.Min(13000, math.Floor(effective/3))
	if pct := parsePrefix(env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"], floatPrefix); pct > 0 && pct <= 100 {
		threshold = math.Min(threshold, math.Floor(effective*pct/100))
	}
	return effective, threshold
}
