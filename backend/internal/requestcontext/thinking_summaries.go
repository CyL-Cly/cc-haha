package requestcontext

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const ThinkingSummaryRedactionBeta = "redact-thinking-2026-02-12"

// The original custom first-party relay branch never enters the memoized
// getAllModelBetas function. In particular, it cannot seed or reuse that cache.
func NativeThinkingSummaryUsesCache(env map[string]string) bool {
	return !nativeCustomBetaRelay(env)
}

func WithThinkingSummaryBeta(values []string, enabled bool) []string {
	return insertThinkingSummaryBeta(values, &enabled)
}

// NativeThinkingSummaryBeta is the original getMergedBetas redaction decision.
// Interactive is the CLI runtime's global mode, independent of a side query's
// request-level isNonInteractiveSession option.
func NativeThinkingSummaryBeta(model string, env map[string]string, interactive bool, settings protocol.JSONObject) bool {
	return NativeModelThinkingSummaryBeta(model, env, interactive, settings, false)
}

// Model validation calls getModelBetas even when getMergedBetas skips a custom
// relay. Its first-party defaults therefore consume the actual subscriber
// identity, while ordinary relay turns retain the getMergedBetas shortcut.
func NativeModelThinkingSummaryBeta(model string, env map[string]string, interactive bool, settings protocol.JSONObject, subscriber bool) bool {
	if !interactive || string(settings.Get("showThinkingSummaries")) == "true" || envTrue(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		return false
	}
	if envTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || envTrue(env["CLAUDE_CODE_USE_VERTEX"]) {
		return false
	}
	foundry := envTrue(env["CLAUDE_CODE_USE_FOUNDRY"])
	customRelay := nativeCustomBetaRelay(env)
	if !foundry && customRelay && !subscriber {
		return false
	}
	isp, present := nativeCapability(Context{Options: Options{Model: model}, Host: Host{Env: env}}, "interleaved_thinking")
	if !foundry && !customRelay {
		// The official first-party provider ignores 3P tier capability overrides.
		present = false
	}
	if !present {
		isp = foundry || !strings.Contains(nativeCanonicalModel(model), "claude-3-")
	}
	return isp
}

func insertThinkingSummaryBeta(values []string, enabled *bool) []string {
	if enabled == nil || !*enabled {
		return values
	}
	for _, value := range values {
		if value == ThinkingSummaryRedactionBeta {
			return values
		}
	}
	index := len(values)
	for i, value := range values {
		if value == "context-management-2025-06-27" || value == "prompt-caching-scope-2026-01-05" {
			index = i
			break
		}
	}
	values = append(values, "")
	copy(values[index+1:], values[index:])
	values[index] = ThinkingSummaryRedactionBeta
	return values
}
