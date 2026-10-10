package requestcontext

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

var nativeTiers = []string{"FABLE", "OPUS", "SONNET", "HAIKU"}

// IsNativePolicyEnvironment names only controls actually consumed by this
// direct-relay request producer. Transport and startup inputs stay with their
// owning services rather than being admitted as inert request options.
func IsNativePolicyEnvironment(name string) bool {
	if supportedHostEnvironment[name] {
		return true
	}
	for _, tier := range nativeTiers {
		if name == "ANTHROPIC_DEFAULT_"+tier+"_MODEL" || name == "ANTHROPIC_DEFAULT_"+tier+"_MODEL_SUPPORTED_CAPABILITIES" {
			return true
		}
	}
	return false
}

func normalizeNativeModelKey(model string) string {
	model = strings.ToLower(trim(model))
	model = strings.TrimSuffix(model, "[1m]")
	return strings.TrimSuffix(model, ":1m")
}

// Source get3PModelCapabilityOverride: pinned identity is exact after stripping
// the context annotation; tier order is observable when models share an ID.
func nativeCapability(input Context, capability string) (bool, bool) {
	key := normalizeNativeModelKey(input.Options.Model)
	for _, tier := range nativeTiers {
		pinned := input.Host.Env["ANTHROPIC_DEFAULT_"+tier+"_MODEL"]
		capabilities, present := input.Host.Env["ANTHROPIC_DEFAULT_"+tier+"_MODEL_SUPPORTED_CAPABILITIES"]
		if pinned == "" || !present || normalizeNativeModelKey(pinned) != key {
			continue
		}
		for _, entry := range strings.Split(strings.ToLower(capabilities), ",") {
			if trim(entry) == capability {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

func nativeCanonicalModel(model string) string {
	name := strings.ToLower(model)
	// Source firstPartyNameToCanonical deliberately resolves Opus 4.6 as 4.
	for _, entry := range []string{"claude-fable-5-1", "claude-fable-5", "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-5", "claude-opus-4-1", "claude-opus-4", "claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4", "claude-haiku-4-5", "claude-3-7-sonnet", "claude-3-5-sonnet", "claude-3-5-haiku", "claude-3-opus", "claude-3-sonnet", "claude-3-haiku"} {
		if strings.Contains(name, entry) {
			return entry
		}
	}
	if match := regexp.MustCompile(`claude-(?:\d+-\d+-)?\w+`).FindString(name); match != "" {
		return match
	}
	return name
}

func nativeModelLimits(model string) (float64, float64) {
	m := nativeCanonicalModel(model)
	switch {
	case m == "claude-opus-5-5" || m == "claude-sonnet-5-5":
		return 128000, 128000
	case containsAny(m, "fable-5", "opus-5", "opus-4-8", "opus-4-7", "opus-4-6", "sonnet-5"):
		return 64000, 128000
	case strings.Contains(m, "sonnet-4-6"):
		return 32000, 128000
	case containsAny(m, "opus-4-5", "sonnet-4", "haiku-4"):
		return 32000, 64000
	case containsAny(m, "opus-4-1", "opus-4"):
		return 32000, 32000
	case strings.Contains(m, "claude-3-opus"):
		return 4096, 4096
	case strings.Contains(m, "claude-3-sonnet"):
		return 8192, 8192
	case strings.Contains(m, "claude-3-haiku"):
		return 4096, 4096
	case containsAny(m, "3-5-sonnet", "3-5-haiku"):
		return 8192, 8192
	case strings.Contains(m, "3-7-sonnet"):
		return 32000, 64000
	default:
		return 32000, 64000
	}
}

// NativeConfiguredMaxOutputTokens shares Source's output budget resolver with
// the request constructor. It reads original controls, never a captured body.
func NativeConfiguredMaxOutputTokens(model string, env map[string]string) float64 {
	fallback, upper := nativeModelLimits(model)
	return maxTokens(Context{Host: Host{Env: env}}, fallback, upper)
}
func containsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
func nativeAdaptiveOnly(model string) bool {
	return strings.Contains(model, "claude-fable-5") || model == "claude-opus-5-5" || model == "claude-sonnet-5-5"
}
func nativeThinkingSupport(input Context) (supported, required, adaptive bool) {
	m := nativeCanonicalModel(input.Options.Model)
	supported, present := nativeCapability(input, "thinking")
	if !present {
		supported = nativeAdaptiveOnly(m) || containsAny(m, "sonnet-4", "opus-4") || containsAny(strings.ToLower(input.Host.Env["ANTHROPIC_BASE_URL"]), "minimax", "minimaxi") && strings.Contains(m, "minimax")
		base := input.Host.Env["ANTHROPIC_BASE_URL"]
		parsed, err := weburl.Parse(base)
		if base == "" || err == nil && parsed.Hostname() == "api.anthropic.com" {
			supported = !strings.Contains(m, "claude-3-")
		}
	}
	required, present = nativeCapability(input, "required_thinking")
	if !present {
		required = nativeAdaptiveOnly(m)
	}
	adaptive, present = nativeCapability(input, "adaptive_thinking")
	if !present {
		adaptive = nativeAdaptiveOnly(m)
	}
	return
}
func nativeResolveThinking(input Context, max, upper float64) (json.RawMessage, bool) {
	canThink, required, supportsAdaptive := nativeThinkingSupport(input)
	adaptive := (required || !envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING"])) && supportsAdaptive
	enabled := required || input.Thinking.Type != "disabled" && !envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_THINKING"])
	hasThinking := (adaptive || required || max > 1024) && enabled
	if hasThinking && canThink {
		if adaptive {
			result := object(field("type", quoted("adaptive")))
			if nativeCanonicalModel(input.Options.Model) == "claude-fable-5-1" {
				result.Set("block_binding", json.RawMessage(`{"prefix_mismatch_behavior":"drop_block"}`))
			}
			return rawObject(result), hasThinking
		}
		budget := upper - 1
		if input.Thinking.Type == "enabled" && input.Thinking.BudgetTokens != nil {
			budget = *input.Thinking.BudgetTokens
		}
		return rawObject(object(field("budget_tokens", rawNumber(math.Min(max-1, budget))), field("type", quoted("enabled")))), hasThinking
	}
	if !hasThinking && (canThink || envTrue(input.Host.Env["CC_HAHA_SEND_DISABLED_THINKING"])) {
		return json.RawMessage(`{"type":"disabled"}`), hasThinking
	}
	return nil, hasThinking
}

func nativeOpenAIModel(model string) bool {
	m := normalizeNativeModelKey(model)
	if at := strings.LastIndex(m, "/"); at >= 0 {
		m = m[at+1:]
	}
	return strings.HasPrefix(m, "gpt-") || regexp.MustCompile(`^o\d`).MatchString(m)
}
func nativeGrokLevels(model string) (bool, bool) {
	switch strings.ToLower(trim(model)) {
	case "grok-4.7", "grok-4.7-build-fast", "grok-4.6":
		return true, true
	case "grok-4.5":
		return true, false
	default:
		return false, false
	}
}
func nativeSupportsEffort(input Context) bool {
	if envTrue(input.Host.Env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"]) {
		return true
	}
	if supported, present := nativeCapability(input, "effort"); present {
		return supported
	}
	if nativeOpenAIModel(input.Options.Model) {
		return true
	}
	grok, _ := nativeGrokLevels(input.Options.Model)
	return grok
}
func nativeSupportsEffortLevel(input Context, level string) bool {
	if supported, present := nativeCapability(input, level+"_effort"); present {
		return supported
	}
	if nativeOpenAIModel(input.Options.Model) {
		return true
	} // Source does not clamp Responses request values.
	_, xhigh := nativeGrokLevels(input.Options.Model)
	return level == "xhigh" && xhigh
}
func nativeEffort(input Context) json.RawMessage {
	requested := input.Options.EffortValue
	requestOverride := input.Options.EffortValueOverridesEnv && requested != nil
	if !requestOverride {
		raw, exists := input.Host.Env["CLAUDE_CODE_EFFORT_LEVEL"]
		if exists && (strings.EqualFold(raw, "unset") || strings.EqualFold(raw, "auto")) {
			return nil
		}
		normalized := strings.ToLower(trim(raw))
		switch normalized {
		case "low", "medium", "high", "xhigh", "max":
			requested = quoted(normalized)
		default:
			if numericEffort.MatchString(normalized) {
				n := jsNumber(normalized)
				if math.Trunc(n) == n && !math.IsInf(n, 0) {
					requested = rawNumber(n)
				}
			}
		}
	}
	if requested == nil && (nativeCanonicalModel(input.Options.Model) == "claude-sonnet-5-5" || nativeCanonicalModel(input.Options.Model) == "claude-opus-5-5") {
		requested = quoted("medium")
	}
	if level := stringValue(requested); (level == "xhigh" || level == "max") && !nativeOpenAIModel(input.Options.Model) && !nativeSupportsEffortLevel(input, level) {
		return quoted("high")
	}
	return requested
}
func nativeConfigureEffort(input Context, output *protocol.JSONObject, betas []string) []string {
	if !nativeSupportsEffort(input) || output.Get("effort") != nil {
		return betas
	}
	effort := nativeEffort(input)
	suppressed := envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) && !localEffortProxy(input.Host.Env["ANTHROPIC_BASE_URL"])
	explicitOpenAI := effort != nil && len(effort) > 0 && effort[0] == '"' && nativeOpenAIModel(input.Options.Model)
	if suppressed && !explicitOpenAI {
		return betas
	}
	if effort == nil {
		if nativeOpenAIModel(input.Options.Model) {
			return betas
		}
		output.Set("effort", quoted("high"))
		return append(betas, "effort-2025-11-24")
	}
	if effort[0] == '"' {
		output.Set("effort", effort)
		if !suppressed {
			betas = append(betas, "effort-2025-11-24")
		}
	}
	return betas // numeric overrides are ant-only; this profile is external.
}
