package sdkhost

import "strings"

func costAuthEnvTruthy(value string) bool {
	switch strings.ToLower(strings.Trim(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// nativeCostSubscriber follows Source's direct-credential short circuit. Once
// Anthropic auth is disabled, isClaudeAISubscriber returns false before reading
// OAuth tokens. An enabled OAuth path still needs the actual scopes/limits store.
func nativeCostSubscriber(env map[string]string) (bool, error) {
	if env["ANTHROPIC_UNIX_SOCKET"] != "" {
		return false, cap("/cost Unix-socket OAuth auth proxy")
	}
	managed := costAuthEnvTruthy(env["CLAUDE_CODE_REMOTE"]) || env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	externalToken := env["ANTHROPIC_AUTH_TOKEN"] != ""
	// The native print host is noninteractive: Source preferThirdPartyAuthentication
	// selects an environment API key without its interactive approval lookup.
	externalKey := env["ANTHROPIC_API_KEY"] != "" && !(env["USER_TYPE"] == "ant" && costAuthEnvTruthy(env["COO_RUNNING_ON_HOMESPACE"]))
	thirdParty := false
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		thirdParty = thirdParty || costAuthEnvTruthy(env[key])
	}
	authEnabled := !(thirdParty || (externalToken || externalKey) && !managed)
	if authEnabled {
		return false, cap("/cost actual Claude AI OAuth scopes and subscription limits")
	}
	return authEnabled, nil
}
