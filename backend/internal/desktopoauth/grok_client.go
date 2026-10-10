package desktopoauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const GrokOAuthIssuer = "https://auth.x.ai"
const GrokOAuthClientID = "b1a00492-073a-47ea-816f-4c329264a828"
const GrokOAuthScope = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write"

func BuildGrokAuthorizeURL(redirectURI, verifier, state, nonce, clientID string) string {
	if clientID = protocol.TrimJavaScriptString(clientID); clientID == "" {
		clientID = GrokOAuthClientID
	}
	challenge := sha256.Sum256([]byte(verifier))
	pairs := [][2]string{{"response_type", "code"}, {"client_id", clientID}, {"redirect_uri", redirectURI}, {"scope", GrokOAuthScope}, {"state", state}, {"nonce", nonce}, {"code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])}, {"code_challenge_method", "S256"}}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, formEscape(pair[0])+"="+formEscape(pair[1]))
	}
	return GrokOAuthIssuer + "/oauth2/authorize?" + strings.Join(parts, "&")
}

func NormalizeGrokTokens(response protocol.JSONObject, nowMillis int64) (protocol.JSONObject, error) {
	if !truthy(response.Get("access_token")) {
		return record(), errors.New("Grok OAuth response did not include an access token")
	}
	if !truthy(response.Get("refresh_token")) {
		return record(), errors.New("Grok OAuth response did not include a refresh token")
	}
	claims := grokTokenClaims(response)
	result := record(field("accessToken", response.Get("access_token")), field("refreshToken", response.Get("refresh_token")), field("expiresAt", numberJSON(float64(nowMillis)+number(coalesce(response.Get("expires_in"), encode(21600)))*1000)))
	if truthy(response.Get("id_token")) {
		result.Set("idToken", response.Get("id_token"))
	}
	if email := object(claims).Get("email"); truthy(email) {
		result.Set("email", email)
	}
	result.Set("clientId", encode(GrokOAuthClientID))
	if truthy(response.Get("scope")) {
		result.Set("scope", response.Get("scope"))
	}
	value := response.Get("token_type")
	if !truthy(value) {
		value = encode("Bearer")
	}
	result.Set("tokenType", value)
	return result, nil
}

func grokTokenClaims(response protocol.JSONObject) json.RawMessage {
	claims := ParseOpenAIJWTClaims(response.String("id_token"))
	if claims == nil || string(claims) == "null" {
		claims = ParseOpenAIJWTClaims(response.String("access_token"))
	}
	return claims
}

func WithRefreshedGrokAccessToken(existing, response protocol.JSONObject, nowMillis int64) protocol.JSONObject {
	result := existing.Clone()
	result.Set("accessToken", response.Get("access_token"))
	result.Set("refreshToken", grokFallback(response.Get("refresh_token"), existing.Get("refreshToken")))
	result.Set("expiresAt", numberJSON(float64(nowMillis)+number(coalesce(response.Get("expires_in"), encode(21600)))*1000))
	result.Set("idToken", grokFallback(response.Get("id_token"), existing.Get("idToken")))
	result.Set("email", grokFallback(object(grokTokenClaims(response)).Get("email"), existing.Get("email")))
	result.Set("clientId", coalesce(existing.Get("clientId"), encode(GrokOAuthClientID)))
	result.Set("scope", grokFallback(response.Get("scope"), existing.Get("scope")))
	result.Set("tokenType", coalesce(response.Get("token_type"), existing.Get("tokenType"), encode("Bearer")))
	return result
}

func grokFallback(value, fallback json.RawMessage) json.RawMessage {
	if value == nil || string(value) == "null" {
		return fallback
	}
	return value
}

func GrokStoredTokens(tokens protocol.JSONObject) protocol.JSONObject {
	result := record()
	for _, name := range []string{"accessToken", "refreshToken", "expiresAt"} {
		result.Set(name, tokens.Get(name))
	}
	for _, name := range []string{"idToken", "email", "clientId"} {
		result.Set(name, coalesce(tokens.Get(name)))
	}
	return result
}
