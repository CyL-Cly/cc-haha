package clibootstrap

// Matches services/analytics/config.ts and utils/privacyLevel.ts. Privacy
// controls test string presence, while provider switches use isEnvTruthy.
func nativeAnalyticsDisabled(environment map[string]string) bool {
	if environment["NODE_ENV"] == "test" || environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" || environment["DISABLE_TELEMETRY"] != "" {
		return true
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthyNative(environment[name]) {
			return true
		}
	}
	return false
}
