// Package analytics owns the original optional file-operation event pipeline.
// Tool results do not depend on successful delivery of these events.
package analytics

import "strings"

func PrivacyLevel(environment map[string]string) string {
	if environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" {
		return "essential-traffic"
	}
	if environment["DISABLE_TELEMETRY"] != "" {
		return "no-telemetry"
	}
	return "default"
}

func Disabled(environment map[string]string) bool {
	if environment["NODE_ENV"] == "test" || PrivacyLevel(environment) != "default" {
		return true
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if EnvTruthy(environment[name]) {
			return true
		}
	}
	return false
}

func EnvTruthy(value string) bool {
	value = strings.ToLower(strings.TrimFunc(value, func(r rune) bool {
		return r == '\ufeff' || r == '\u00a0' || r == '\u2028' || r == '\u2029' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == ' ' || r >= '\t' && r <= '\r'
	}))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
