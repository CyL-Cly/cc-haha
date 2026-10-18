package mcpruntime

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) AuthenticateWithOptions(ctx context.Context, name string, config protocol.JSONObject, onURL func(string) error, options mcpauth.FlowOptions) error {
	if s == nil || s.options.AuthenticateFlow == nil {
		return errors.New("native MCP OAuth flow options unavailable")
	}
	return s.options.AuthenticateFlow(ctx, name, config, onURL, options)
}
func (s *Service) HasOAuthTokens(ctx context.Context, name string, config protocol.JSONObject) (bool, error) {
	if s == nil || s.options.HasOAuthTokens == nil {
		return false, errors.New("native MCP OAuth credential presence unavailable")
	}
	return s.options.HasOAuthTokens(ctx, name, config)
}
func (s *Service) RevokeOAuth(ctx context.Context, name string, config protocol.JSONObject, preserve bool) error {
	if s == nil || s.options.RevokeOAuth == nil {
		return errors.New("native MCP OAuth revocation unavailable")
	}
	return s.options.RevokeOAuth(ctx, name, config, preserve)
}
