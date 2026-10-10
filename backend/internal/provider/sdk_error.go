package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NewSDKResponseError keeps one independent projection at the policy and notice
// sites. Missing originals are rejected rather than inferred from a Header map.
func NewSDKResponseError(status *int, data string, headers *BunHeaders, httpFailure bool) error {
	if headers == nil {
		return ErrBunHeaderMetadata
	}
	failure := sdkAPIError(status, data, headers.HTTPHeader(), httpFailure)
	// APIError keeps the Headers object itself; RequestID is cached above.
	failure.OriginalHeaders = headers
	return failure
}

func sdkAPIError(status *int, data string, headers http.Header, httpFailure bool) *SDKStreamError {
	if headers == nil {
		headers = make(http.Header)
	}
	raw := []byte(data)
	var payload json.RawMessage
	if json.Valid(raw) || len(sdkJSONSyntaxUnits(data)) == 0 {
		payload = append(json.RawMessage(nil), raw...)
	}
	if !httpFailure && (payload == nil || bytes.Equal(bytes.TrimSpace(payload), []byte("null"))) {
		payload, _ = json.Marshal(data) // safeJSON(data) ?? data
	}
	var messageUnits []uint16
	if selected := sdkJSONTopLevelField(payload, "message"); jsonTruthy(selected) {
		if bytes.HasPrefix(bytes.TrimSpace(selected), []byte(`"`)) {
			messageUnits, _ = protocol.DecodeJavaScriptJSONString(selected)
		} else {
			serialized := protocol.StringifyValidatedJavaScriptJSON(selected)
			messageUnits = utf16.Encode([]rune(string(serialized)))
		}
	} else if jsonTruthy(payload) {
		serialized := protocol.StringifyValidatedJavaScriptJSON(payload)
		messageUnits = utf16.Encode([]rune(string(serialized)))
	} else if httpFailure && !jsonTruthy(payload) {
		messageUnits = utf16.Encode([]rune(data)) // SDK errMessage for falsy/invalid JSON
	}
	class, kind := "APIError", "sse_api"
	if status != nil {
		kind = "http_api"
		class = sdkHTTPErrorClass(*status)
		if len(messageUnits) == 0 {
			messageUnits = utf16.Encode([]rune(strconv.Itoa(*status) + " status code (no body)"))
		} else {
			messageUnits = append(utf16.Encode([]rune(strconv.Itoa(*status)+" ")), messageUnits...)
		}
	} else if len(messageUnits) == 0 {
		messageUnits = utf16.Encode([]rune("(no status code or body)"))
	}
	return &SDKStreamError{
		Kind: kind, Class: class, Name: "Error", Message: string(utf16.Decode(messageUnits)),
		MessageJSON: protocol.QuoteJavaScriptUTF16(messageUnits), Status: status,
		RequestID: sdkRequestID(headers), Payload: payload, RawData: data, Headers: headers.Clone(),
	}
}

func sdkHTTPErrorClass(status int) string {
	switch status {
	case 400:
		return "BadRequestError"
	case 401:
		return "AuthenticationError"
	case 403:
		return "PermissionDeniedError"
	case 404:
		return "NotFoundError"
	case 409:
		return "ConflictError"
	case 422:
		return "UnprocessableEntityError"
	case 429:
		return "RateLimitError"
	default:
		if status >= 500 {
			return "InternalServerError"
		}
		return "APIError"
	}
}

// sdkJSONSyntaxError uses the pinned runtime's JSON.parse diagnostic profile.
// Valid values never pass through this helper or lose their raw numeric input.
func sdkJSONSyntaxError(data string) *SDKStreamError {
	units := sdkJSONSyntaxUnits(data)
	if len(units) == 0 {
		return nil
	}
	message := string(utf16.Decode(units))
	raw := protocol.QuoteJavaScriptUTF16(units)
	return &SDKStreamError{Kind: "json_parse", Class: "SyntaxError", Name: "SyntaxError", Message: message, MessageJSON: raw, RawData: data}
}

func sdkJSONParseMessage(data string) string {
	return string(utf16.Decode(sdkJSONSyntaxUnits(data)))
}
