package desktopoauth

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) exchangeGrokCode(ctx context.Context, code, redirectURI, verifier string) (protocol.JSONObject, error) {
	return s.desktopTokenRequest(ctx, "exchange", "Grok", GrokOAuthIssuer+"/oauth2/token", "cc-haha-grok-oauth/1.0", [][2]string{{"grant_type", "authorization_code"}, {"client_id", GrokOAuthClientID}, {"code", code}, {"redirect_uri", redirectURI}, {"code_verifier", verifier}})
}

func (s *Service) refreshGrokTokens(ctx context.Context, token string) (protocol.JSONObject, error) {
	return s.desktopTokenRequest(ctx, "refresh", "Grok", GrokOAuthIssuer+"/oauth2/token", "cc-haha-grok-oauth/1.0", [][2]string{{"grant_type", "refresh_token"}, {"client_id", GrokOAuthClientID}, {"refresh_token", token}})
}
