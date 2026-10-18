package desktopoauth

import "strings"

// ClaudeAIOrigin is CLAUDE_AI_ORIGIN in the same getOauthConfig profile.
// Production's connector frontend differs from its authorize host.
func ClaudeAIOrigin(env map[string]string) (string, error) {
	config, err := ResolveConfig(env)
	if err != nil {
		return "", err
	}
	if config.FileSuffix == "" {
		return "https://claude.ai", nil
	}
	return strings.TrimSuffix(config.ClaudeAuthorizeURL, "/oauth/authorize"), nil
}
