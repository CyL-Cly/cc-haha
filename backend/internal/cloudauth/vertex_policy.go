package cloudauth

import "strings"

func VertexRegion(model string, environment map[string]string) string {
	fallback := environment["CLOUD_ML_REGION"]
	if fallback == "" {
		fallback = "us-east5"
	}
	for _, row := range [][2]string{{"claude-haiku-4-5", "VERTEX_REGION_CLAUDE_HAIKU_4_5"}, {"claude-3-5-haiku", "VERTEX_REGION_CLAUDE_3_5_HAIKU"}, {"claude-3-5-sonnet", "VERTEX_REGION_CLAUDE_3_5_SONNET"}, {"claude-3-7-sonnet", "VERTEX_REGION_CLAUDE_3_7_SONNET"}, {"claude-opus-4-1", "VERTEX_REGION_CLAUDE_4_1_OPUS"}, {"claude-opus-4", "VERTEX_REGION_CLAUDE_4_0_OPUS"}, {"claude-sonnet-4-6", "VERTEX_REGION_CLAUDE_4_6_SONNET"}, {"claude-sonnet-4-5", "VERTEX_REGION_CLAUDE_4_5_SONNET"}, {"claude-sonnet-4", "VERTEX_REGION_CLAUDE_4_0_SONNET"}} {
		if strings.HasPrefix(model, row[0]) {
			if environment[row[1]] != "" {
				return environment[row[1]]
			}
			return fallback
		}
	}
	return fallback
}
func VertexBetas(model, canonical string, environment map[string]string, sdk []string) []string {
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
	if strings.Contains(canonical, "claude-opus-4") || strings.Contains(canonical, "claude-sonnet-4") || strings.Contains(canonical, "claude-haiku-4") {
		all = append(all, "web-search-2025-03-05")
	}
	for _, beta := range strings.Split(environment["ANTHROPIC_BETAS"], ",") {
		if beta = strings.TrimFunc(beta, jsSpace); beta != "" {
			all = append(all, beta)
		}
	}
	found := func(values []string, value string) bool {
		for _, entry := range values {
			if entry == value {
				return true
			}
		}
		return false
	}
	if !found(all, "claude-code-20250219") {
		all = append(all, "claude-code-20250219")
	}
	base := append([]string{}, all...)
	for _, beta := range sdk {
		if !found(base, beta) {
			all = append(all, beta)
		}
	}
	return all
}
