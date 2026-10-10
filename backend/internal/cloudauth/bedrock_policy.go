package cloudauth

import (
	"strings"
)

var bedrockBodyBeta = map[string]bool{"interleaved-thinking-2025-05-14": true, "context-1m-2025-08-07": true, "tool-search-tool-2025-10-19": true}

// BedrockBetas ports getModelBetas/getMergedBetas and the separate
// getBedrockExtraBodyParamsBetas producer. Canonical is obtained from the actual
// shared model resolver; it must not be guessed from an ARN or provider alias.
func BedrockBetas(model, canonical string, environment map[string]string, sdkBetas []string, agentic bool) (headers, extra []string) {
	all := []string{}
	if !strings.Contains(canonical, "haiku") {
		all = append(all, "claude-code-20250219")
	}
	if !cloudTruthy(environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (strings.Contains(strings.ToLower(model), "[1m]") || strings.HasSuffix(strings.ToLower(model), ":1m")) {
		all = append(all, "context-1m-2025-08-07")
	}
	isp, present := bedrockISPCapability(model, environment)
	if !present {
		isp = strings.Contains(canonical, "claude-opus-4") || strings.Contains(canonical, "claude-sonnet-4")
	}
	if isp && !cloudTruthy(environment["DISABLE_INTERLEAVED_THINKING"]) {
		all = append(all, "interleaved-thinking-2025-05-14")
	}
	for _, beta := range strings.Split(environment["ANTHROPIC_BETAS"], ",") {
		if value := strings.TrimFunc(beta, jsSpace); value != "" {
			all = append(all, value)
		}
	}
	headers, extra = []string{}, []string{}
	for _, beta := range all {
		if bedrockBodyBeta[beta] {
			extra = append(extra, beta)
		} else {
			headers = append(headers, beta)
		}
	}
	contains := func(values []string, value string) bool {
		for _, entry := range values {
			if entry == value {
				return true
			}
		}
		return false
	}
	if agentic && !contains(headers, "claude-code-20250219") {
		headers = append(headers, "claude-code-20250219")
	}
	// Source compares every SDK beta against the original base list. Repeated
	// new SDK entries are retained, rather than deduplicated with each other.
	base := append([]string{}, headers...)
	for _, beta := range sdkBetas {
		if !contains(base, beta) {
			headers = append(headers, beta)
		}
	}
	return headers, extra
}
func cloudTruthy(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, jsSpace)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func bedrockISPCapability(model string, env map[string]string) (bool, bool) {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimFunc(value, jsSpace))
		value = strings.TrimSuffix(value, "[1m]")
		return strings.TrimSuffix(value, ":1m")
	}
	for _, tier := range []string{"FABLE", "OPUS", "SONNET", "HAIKU"} {
		pinned := env["ANTHROPIC_DEFAULT_"+tier+"_MODEL"]
		capabilities, present := env["ANTHROPIC_DEFAULT_"+tier+"_MODEL_SUPPORTED_CAPABILITIES"]
		if pinned == "" || !present || normalize(pinned) != normalize(model) {
			continue
		}
		for _, value := range strings.Split(strings.ToLower(capabilities), ",") {
			if strings.TrimFunc(value, jsSpace) == "interleaved_thinking" {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}
