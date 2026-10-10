package openaiauth

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type AnthropicAuthInput struct {
	Bare         bool
	Environment  map[string]string
	APIKeySource string
	APIKeyHelper json.RawMessage
}

func authEnvTruthy(value string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func rawAuthTruthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case 't', '[', '{':
		return true
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		return err == nil && len(units) > 0
	default:
		n, _ := strconv.ParseFloat(string(raw), 64)
		return n != 0 && !math.IsNaN(n)
	}
}
func AuthTruthy(raw json.RawMessage) bool { return rawAuthTruthy(raw) }

// AnthropicAuthEnabled consumes the actual API-key source category, preserving
// managed desktop/remote exceptions. It does not infer a source from model IDs.
func AnthropicAuthEnabled(in AnthropicAuthInput) bool {
	env := in.Environment
	if in.Bare {
		return false
	}
	if env["ANTHROPIC_UNIX_SOCKET"] != "" {
		return env["CLAUDE_CODE_OAUTH_TOKEN"] != ""
	}
	third := false
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		third = third || authEnvTruthy(env[key])
	}
	managed := authEnvTruthy(env["CLAUDE_CODE_REMOTE"]) || env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	externalToken := env["ANTHROPIC_AUTH_TOKEN"] != "" || rawAuthTruthy(in.APIKeyHelper) || env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"] != ""
	externalKey := in.APIKeySource == "ANTHROPIC_API_KEY" || in.APIKeySource == "apiKeyHelper"
	return !(third || externalToken && !managed || externalKey && !managed)
}

func ClaudeTokensFromSecure(raw json.RawMessage) *Value {
	var root protocol.JSONObject
	if root.UnmarshalJSON(raw) != nil || !root.IsObject() {
		return nil
	}
	token, err := NewValue(root.Get("claudeAiOauth"))
	if err != nil || token == nil || !rawAuthTruthy(token.Get("accessToken")) {
		return nil
	}
	return token
}
func ClaudeEnvironmentToken(access string) *Value {
	return ObjectValue(protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: quoteAuth(access)}, protocol.JSONField{Name: "refreshToken", Value: json.RawMessage("null")}, protocol.JSONField{Name: "expiresAt", Value: json.RawMessage("null")}, protocol.JSONField{Name: "scopes", Value: json.RawMessage(`["user:inference"]`)}, protocol.JSONField{Name: "subscriptionType", Value: json.RawMessage("null")}, protocol.JSONField{Name: "rateLimitTier", Value: json.RawMessage("null")}))
}
func quoteAuth(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func ClaudeSubscriber(enabled bool, tokens *Value) (bool, error) {
	if !enabled || tokens == nil {
		return false, nil
	}
	scopes := bytes.TrimSpace(tokens.Get("scopes"))
	if len(scopes) == 0 || bytes.Equal(scopes, []byte("null")) {
		return false, nil
	}
	if scopes[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(scopes)
		if err != nil {
			return false, err
		}
		return strings.Contains(string(utf16.Decode(units)), "user:inference"), nil
	}
	if scopes[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(scopes, &values); err != nil {
			return false, err
		}
		for _, value := range values {
			if len(value) > 0 && value[0] == '"' {
				units, err := protocol.DecodeJavaScriptJSONString(value)
				if err == nil && string(utf16.Decode(units)) == "user:inference" {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, &ClaudeScopesTypeError{}
}

type ClaudeScopesTypeError struct{}

func (*ClaudeScopesTypeError) Error() string {
	return "scopes?.includes is not a function. (In 'scopes?.includes(CLAUDE_AI_INFERENCE_SCOPE)', 'scopes?.includes' is undefined)"
}
func (*ClaudeScopesTypeError) ErrorName() string { return "TypeError" }
