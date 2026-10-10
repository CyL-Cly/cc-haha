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

func GrokRefreshRequest(refreshToken, clientID json.RawMessage) (OpenAITokenRequest, error) {
	quoted, err := protocol.ExactJavaScriptStringJSON(refreshToken)
	if err != nil {
		return OpenAITokenRequest{}, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(quoted)
	if err != nil {
		return OpenAITokenRequest{}, err
	}
	client := GrokOAuthClientID
	if len(clientID) > 0 && string(clientID) != "null" {
		var value string
		if err := json.Unmarshal(clientID, &value); err != nil {
			return OpenAITokenRequest{}, err
		}
		if value = protocol.TrimJavaScriptString(value); value != "" {
			client = value
		}
	}
	pairs := [][2]string{{"grant_type", "refresh_token"}, {"client_id", client}, {"refresh_token", string(utf16.Decode(units))}}
	parts := []string{}
	for _, pair := range pairs {
		parts = append(parts, formEscape(pair[0])+"="+formEscape(pair[1]))
	}
	return OpenAITokenRequest{URL: GrokOAuthIssuer + "/oauth2/token", Method: "POST", Body: strings.Join(parts, "&"), Headers: [][2]string{{"Accept", "application/json"}, {"Content-Type", "application/x-www-form-urlencoded"}, {"User-Agent", "cc-haha-grok-oauth/1.0"}}}, nil
}
func DecodeGrokRefreshResponse(status int, body []byte, readError error) (protocol.JSONObject, error) {
	if status < 200 || status >= 300 {
		if readError != nil {
			body = nil
		}
		message := sanitizeOpenAITokenErrorBody(string(protocol.DecodeJavaScriptUTF8(body)))
		suffix := ""
		if message != "" {
			suffix = ": " + message
		}
		return protocol.JSONObject{}, fmt.Errorf("Grok token refresh failed: %d%s", status, suffix)
	}
	if readError != nil {
		return protocol.JSONObject{}, readError
	}
	decoded := bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(body), []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(decoded) {
		return protocol.JSONObject{}, errors.New("invalid Grok token JSON response")
	}
	var value protocol.JSONObject
	err := value.UnmarshalJSON(decoded)
	return value, err
}
