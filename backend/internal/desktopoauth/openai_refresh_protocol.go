package desktopoauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type OpenAITokenRequest struct {
	URL, Method, Body string
	Headers           [][2]string
}

// OpenAIRefreshRequest is only the original HTTP protocol producer. The owning
// CLI/provider supplies actual TLS, proxy, timeout, Trace and response lifetime.
func OpenAIRefreshRequest(refreshToken json.RawMessage) (OpenAITokenRequest, error) {
	quoted, err := protocol.ExactJavaScriptStringJSON(refreshToken)
	if err != nil {
		return OpenAITokenRequest{}, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(quoted)
	if err != nil {
		return OpenAITokenRequest{}, err
	}
	// URLSearchParams converts to USVString, replacing unpaired UTF-16 units.
	value := string(utf16.Decode(units))
	pairs := [][2]string{{"grant_type", "refresh_token"}, {"refresh_token", value}, {"client_id", OpenAICodexClientID}, {"scope", "openid profile email"}}
	parts := make([]string, len(pairs))
	for i, pair := range pairs {
		parts[i] = formEscape(pair[0]) + "=" + formEscape(pair[1])
	}
	return OpenAITokenRequest{URL: OpenAIAuthIssuer + "/oauth/token", Method: "POST", Body: strings.Join(parts, "&"), Headers: [][2]string{{"Accept", "application/json"}, {"Content-Type", "application/x-www-form-urlencoded"}, {"User-Agent", OpenAICodexTokenUserAgent}}}, nil
}

// DecodeOpenAITokenResponse returns original JSON roots without imposing an
// account schema. Non-2xx response.text failures become an empty error suffix;
// successful response.json failures remain real failures. Secrets are redacted
// using the original pinned token-error rules before an error is returned.
func DecodeOpenAITokenResponse(status int, body []byte, readError error) (json.RawMessage, error) {
	if status < 200 || status >= 300 {
		if readError != nil {
			body = nil
		}
		text := sanitizeOpenAITokenErrorBody(string(protocol.DecodeJavaScriptUTF8(body)))
		suffix := ""
		if text != "" {
			suffix = ": " + text
		}
		return nil, fmt.Errorf("OpenAI token refresh failed: %d%s", status, suffix)
	}
	if readError != nil {
		return nil, readError
	}
	decoded := bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(body), []byte{0xef, 0xbb, 0xbf})
	// Retain numeric lexemes until the caller's literal/property operations;
	// JSON.parse accepts overflowing JSON numbers before JSON.stringify later
	// serializes their Infinity value as null.
	if !json.Valid(decoded) {
		return nil, errors.New("invalid OpenAI token JSON response")
	}
	return append(json.RawMessage(nil), decoded...), nil
}
