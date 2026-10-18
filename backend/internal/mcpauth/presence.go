package mcpauth

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// HasTokens executes the same ClaudeAuthProvider.tokens producer used by the
// MCPSettings prepareServers effect. This may refresh an MCP token, just as a
// normal request does; it never initiates browser consent.
func (s *Service) HasTokens(ctx context.Context, name string, config protocol.JSONObject) (bool, error) {
	provider, err := s.Provider(ctx, name, config)
	if err != nil {
		return false, err
	}
	tokens, err := provider.Tokens(ctx)
	if err != nil {
		return false, err
	}
	return tokens != nil, nil
}
