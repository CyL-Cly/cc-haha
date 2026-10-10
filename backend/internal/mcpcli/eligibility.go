package mcpcli

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ClaudeAIEligibility follows fetchClaudeAIMcpConfigsIfEligible's own scope
// predicate. An API key does not suppress eligible OAuth connectors.
func ClaudeAIRequiresSecure(environment map[string]string, bare bool) (bool, error) {
	if value, defined := environment["ENABLE_CLAUDEAI_MCP_SERVERS"]; defined {
		switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
		case "0", "false", "no", "off":
			return false, nil
		}
	}
	if bare || envTruthy(environment["CLAUDE_CODE_SIMPLE"]) || environment["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		return false, nil
	}
	if environment["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] != "" {
		return false, errors.New("CLI MCP capability is not migrated: Claude OAuth descriptor token precedence")
	}
	return true, nil
}

func ClaudeAIEligibility(environment map[string]string, bare bool, secure json.RawMessage) (bool, error) {
	needsSecure, err := ClaudeAIRequiresSecure(environment, bare)
	if err != nil || !needsSecure {
		return false, err
	}
	tokens := openaiauth.ClaudeTokensFromSecure(secure)
	if tokens == nil {
		return false, nil
	}
	scopes := tokens.Get("scopes")
	if len(scopes) == 0 || string(scopes) == "null" {
		return false, nil
	}
	if units, err := protocol.DecodeJavaScriptJSONString(scopes); err == nil {
		raw, _ := json.Marshal("user:mcp_servers")
		wanted, _ := protocol.DecodeJavaScriptJSONString(raw)
		return strings.Contains(stringUTF16ForMCP(units), stringUTF16ForMCP(wanted)), nil
	}
	var values []json.RawMessage
	if json.Unmarshal(scopes, &values) != nil {
		// fetchClaudeAIMcpConfigsIfEligible catches malformed .includes and
		// returns no organization configurations, rather than rejecting CLI IO.
		return false, nil
	}
	for _, value := range values {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err == nil && stringUTF16ForMCP(units) == "user:mcp_servers" {
			return true, nil
		}
	}
	return false, nil
}
func stringUTF16ForMCP(units []uint16) string {
	var value strings.Builder
	for _, u := range units {
		value.WriteRune(rune(u))
	}
	return value.String()
}
