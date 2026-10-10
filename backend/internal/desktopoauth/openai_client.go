package desktopoauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These are the original bundled client's protocol constants. The account
// service and CLI consumer share them rather than selecting endpoints from env.
const OpenAIAuthIssuer = "https://auth.openai.com"
const OpenAICodexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const OpenAICodexOAuthPort = 1455
const OpenAICodexRedirectPath = "/auth/callback"
const OpenAICodexTokenUserAgent = "codex-cli/0.157.0"

func BuildOpenAIAuthorizeURL(redirectURI, verifier, state string) string {
	challenge := sha256.Sum256([]byte(verifier))
	pairs := [][2]string{{"response_type", "code"}, {"client_id", OpenAICodexClientID}, {"redirect_uri", redirectURI}, {"scope", "openid profile email offline_access"}, {"code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])}, {"code_challenge_method", "S256"}, {"id_token_add_organizations", "true"}, {"codex_cli_simplified_flow", "true"}, {"state", state}}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, formEscape(pair[0])+"="+formEscape(pair[1]))
	}
	return OpenAIAuthIssuer + "/oauth/authorize?" + strings.Join(parts, "&")
}

// JWT claims are display/account metadata, not a local authorization decision.
// Source merely decodes the middle segment; issuer signature validation belongs
// to the official token endpoint, which produces these tokens.
func ParseOpenAIJWTClaims(token string) json.RawMessage {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || token == "" {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(data))
	if err != nil {
		return nil
	}
	return normalized
}

func openAIAccountID(raw json.RawMessage) json.RawMessage {
	if !truthy(raw) {
		return nil
	}
	claims := object(raw)
	for _, value := range []json.RawMessage{claims.Get("chatgpt_account_id"), object(claims.Get("https://api.openai.com/auth")).Get("chatgpt_account_id")} {
		if truthy(value) {
			return value
		}
	}
	var organizations []json.RawMessage
	if json.Unmarshal(claims.Get("organizations"), &organizations) == nil && len(organizations) > 0 {
		return object(organizations[0]).Get("id")
	}
	return nil
}

func OpenAITokenExpired(expiresAt json.RawMessage, nowMillis int64) bool {
	return number(expiresAt)-float64(nowMillis) <= 5*60*1000
}

func NormalizeOpenAITokens(response protocol.JSONObject, nowMillis int64) (protocol.JSONObject, error) {
	if !truthy(response.Get("refresh_token")) {
		return record(), errors.New("OpenAI OAuth response did not include a refresh token")
	}
	return normalizedOpenAITokens(response, nil, nowMillis), nil
}

func WithRefreshedOpenAIAccessToken(existing, refreshed protocol.JSONObject, nowMillis int64) protocol.JSONObject {
	return normalizedOpenAITokens(refreshed, &existing, nowMillis)
}

func normalizedOpenAITokens(response protocol.JSONObject, existing *protocol.JSONObject, nowMillis int64) protocol.JSONObject {
	claims := ParseOpenAIJWTClaims(response.String("id_token"))
	if claims == nil || string(claims) == "null" {
		claims = ParseOpenAIJWTClaims(response.String("access_token"))
	}
	result := record()
	result.Set("accessToken", response.Get("access_token"))
	result.Set("refreshToken", response.Get("refresh_token"))
	result.Set("expiresAt", numberJSON(float64(nowMillis)+number(coalesce(response.Get("expires_in"), encode(3600)))*1000))
	result.Set("idToken", response.Get("id_token"))
	result.Set("accountId", openAIAccountID(claims))
	// The original object literal retains the email insertion slot even when
	// claims are absent, before a refresh fills the existing account's email.
	result.Set("email", object(claims).Get("email"))
	result.Set("clientId", encode(OpenAICodexClientID))
	if existing != nil {
		for _, name := range []string{"refreshToken", "idToken", "accountId", "email"} {
			value := result.Get(name)
			if value == nil || string(value) == "null" {
				result.Set(name, existing.Get(name))
			}
		}
		result.Set("clientId", coalesce(existing.Get("clientId"), encode(OpenAICodexClientID)))
	}
	return result
}

// The desktop store materializes absent optional fields as null. It retains
// the Source shape, with no schema version or account slot shared with Claude.
func OpenAIStoredTokens(tokens protocol.JSONObject) protocol.JSONObject {
	result := record()
	for _, name := range []string{"accessToken", "refreshToken", "expiresAt"} {
		result.Set(name, tokens.Get(name))
	}
	for _, name := range []string{"idToken", "email", "accountId", "clientId"} {
		result.Set(name, coalesce(tokens.Get(name)))
	}
	return result
}
