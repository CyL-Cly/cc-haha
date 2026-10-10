// Package access preserves the local desktop, pet and H5 server boundaries.
// Callers supply credentials and validators explicitly; it never reads a
// developer's environment, settings or keychain during construction.
package access

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"unicode"
)

const LocalAccessTokenEnv = "CC_HAHA_LOCAL_ACCESS_TOKEN"
const PetAccessTokenEnv = "CC_HAHA_PET_ACCESS_TOKEN"

type AuthResult struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

type Authorization struct {
	APIKey, LocalToken, PetToken string
	ValidateH5Token              func(context.Context, string) (bool, error)
}

// JavaScript trim includes BOM and excludes the NEL character. Configured
// process tokens are trimmed; candidate tokens and API keys are not.
func trimConfiguredToken(token string) string {
	return strings.TrimFunc(token, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Zs, r)
	})
}

func parseBearerToken(header string) (AuthResult, string) {
	if header == "" {
		return AuthResult{Error: "Missing Authorization header"}, ""
	}
	parts := strings.Split(header, " ")
	// The TS reference destructures the first two split items, so a third
	// item is ignored. Fields() would incorrectly accept tabs/double spaces.
	if len(parts) < 2 || parts[0] != "Bearer" || parts[1] == "" {
		return AuthResult{Error: "Invalid Authorization format. Use: Bearer <token>"}, ""
	}
	return AuthResult{Valid: true}, parts[1]
}

func candidateToken(header string, override *string) string {
	if override != nil {
		return *override
	}
	_, token := parseBearerToken(header)
	return token
}

func processTokenAuthorized(configured, header string, override *string) bool {
	expected := trimConfiguredToken(configured)
	if expected == "" {
		return false
	}
	candidate := candidateToken(header, override)
	return candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

// Fetch Headers.get combines duplicate values and normalizes outer HTTP
// whitespace before each credential parser runs. Reuse the H5 Fetch boundary
// so local, pet, API and H5 validators consume the same original header.
func authorizationHeader(headers http.Header) string {
	value, _ := h5Header(headers, "Authorization")
	return value
}

func (auth Authorization) HasLocalToken() bool { return trimConfiguredToken(auth.LocalToken) != "" }
func (auth Authorization) HasPetToken() bool   { return trimConfiguredToken(auth.PetToken) != "" }

func (auth Authorization) LocalAuthorized(headers http.Header, override *string) bool {
	return processTokenAuthorized(auth.LocalToken, authorizationHeader(headers), override)
}

func (auth Authorization) PetAuthorized(headers http.Header, override *string) bool {
	return processTokenAuthorized(auth.PetToken, authorizationHeader(headers), override)
}

func (auth Authorization) ValidateAPIKey(headers http.Header) AuthResult {
	parsed, token := parseBearerToken(authorizationHeader(headers))
	if !parsed.Valid {
		return parsed
	}
	if auth.APIKey == "" {
		return AuthResult{Error: "Server ANTHROPIC_API_KEY not configured"}
	}
	if token != auth.APIKey {
		return AuthResult{Error: "Invalid API key"}
	}
	return AuthResult{Valid: true}
}

func (auth Authorization) ValidateRequest(ctx context.Context, headers http.Header, override *string) (AuthResult, error) {
	if auth.LocalAuthorized(headers, override) {
		return AuthResult{Valid: true}, nil
	}
	anthropic := auth.ValidateAPIKey(headers)
	if anthropic.Valid {
		return anthropic, nil
	}
	token := candidateToken(authorizationHeader(headers), override)
	if token == "" {
		return anthropic, nil
	}
	return auth.validateH5(ctx, token)
}

func (auth Authorization) RequireH5(ctx context.Context, headers http.Header, override *string) (AuthResult, error) {
	token := candidateToken(authorizationHeader(headers), override)
	if token == "" {
		return AuthResult{Error: "Missing H5 access token"}, nil
	}
	return auth.validateH5(ctx, token)
}

func (auth Authorization) validateH5(ctx context.Context, token string) (AuthResult, error) {
	if auth.ValidateH5Token == nil {
		return AuthResult{}, errors.New("H5 token validator is required")
	}
	valid, err := auth.ValidateH5Token(ctx, token)
	if err != nil {
		return AuthResult{}, err
	}
	if !valid {
		return AuthResult{Error: "Invalid H5 access token"}, nil
	}
	return AuthResult{Valid: true}, nil
}

// Rejection is nil on success, matching the TS requireAuth/requireH5Token
// middleware. The server's HTTP writer owns JSON encoding and content headers.
type AuthRejection struct {
	Status int               `json:"status"`
	Body   map[string]string `json:"body"`
}

func RejectUnauthorized(result AuthResult) *AuthRejection {
	if result.Valid {
		return nil
	}
	return &AuthRejection{Status: http.StatusUnauthorized, Body: map[string]string{"error": "Unauthorized", "message": result.Error}}
}
