package providerproxy

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const OpenAICodexStreamMarkerHeader = "x-cc-haha-openai-codex-stream"

var openAIIntegerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

// GetOpenAIPolicyError follows only the original SDK/wrapper boundaries and
// explicit upstream codes. JSON inputs cannot have cyclic object identity.
func GetOpenAIPolicyError(input json.RawMessage) (protocol.JSONObject, bool) {
	return openAIPolicyError(input, 0)
}

func openAIPolicyError(raw json.RawMessage, depth int) (protocol.JSONObject, bool) {
	if depth > 12 || raw == nil || string(raw) == "null" || !requestObject(raw).IsObject() && !requestIsArray(raw) {
		return protocol.JSONObject{}, false
	}
	record := requestObject(raw)
	code := record.String("code")
	if code == "cyber_policy" || code == "content_policy" || code == "content_policy_violation" {
		message := record.Get("message")
		if !requestIsString(message) || !requestTruthy(openAITrimJSON(message)) {
			message = requestEncode("Request rejected by the upstream safety policy.")
		}
		return requestRecord(requestField("code", code), requestField("message", message)), true
	}
	for _, key := range []string{"error", "response", "originalError", "cause"} {
		if match, ok := openAIPolicyError(record.Get(key), depth+1); ok {
			return match, true
		}
	}
	if message := record.Get("message"); requestIsString(message) {
		units, _ := protocol.DecodeJavaScriptJSONString(message)
		// The ASCII status prefix is removed before JS trim; keep any lone
		// surrogate in the serialized body's string values at the parse boundary.
		first := 0
		if len(units) > 3 && units[0] >= '0' && units[0] <= '9' && units[1] >= '0' && units[1] <= '9' && units[2] >= '0' && units[2] <= '9' && protocol.IsJavaScriptWhitespace(rune(units[3])) {
			first = 3
			for first < len(units) && protocol.IsJavaScriptWhitespace(rune(units[first])) {
				first++
			}
		}
		trimmed := openAITrimJSON(protocol.QuoteJavaScriptUTF16(units[first:]))
		parsedUnits, _ := protocol.DecodeJavaScriptJSONString(trimmed)
		if len(parsedUnits) > 0 && parsedUnits[0] == '{' {
			text := protocol.JavaScriptJSONSourceUTF16(parsedUnits)
			if json.Valid([]byte(text)) {
				return openAIPolicyError([]byte(text), depth+1)
			}
		}
	}
	return protocol.JSONObject{}, false
}

func ResolveOpenAICodexFirstTokenTimeout(marker string, fallback float64, override string) float64 {
	if marker != "1" {
		return fallback
	}
	value := openAIIntegerPrefix.FindString(protocol.TrimJavaScriptString(override))
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && !math.IsInf(parsed, 0) && parsed > 0 {
		return parsed
	}
	return math.Max(fallback, 300000)
}

func CanRetryOpenAICodexBufferedStream(marker string, crossedSideEffect bool) bool {
	return marker == "1" && !crossedSideEffect
}

// OpenAIUpstreamError is the actual fetch wrapper's HTTP error producer. The
// original response text uses a UTF-16 slice, including an incomplete surrogate.
func OpenAIUpstreamError(status int, text json.RawMessage, requestID string) (int, protocol.JSONObject, protocol.JSONObject) {
	if policy, ok := GetOpenAIPolicyError(requestEncode(requestRecord(requestField("message", text)))); ok {
		errorBody := requestRecord(requestField("type", "permission_error"))
		errorBody.Assign(policy)
		headers := requestRecord(requestField("x-should-retry", "false"))
		if requestID != "" {
			headers.Set("x-request-id", requestEncode(requestID))
		}
		return 403, requestRecord(requestField("type", "error"), requestField("error", errorBody)), headers
	}
	units, _ := protocol.DecodeJavaScriptJSONString(text)
	if len(units) > 500 {
		units = units[:500]
	}
	message := requestJoin([]json.RawMessage{requestEncode("OpenAI upstream returned HTTP " + strconv.Itoa(status) + ": "), protocol.QuoteJavaScriptUTF16(units)}, "")
	return status, requestRecord(requestField("type", "error"), requestField("error", requestRecord(requestField("type", "api_error"), requestField("message", message)))), requestRecord()
}

func OpenAIRequestCompressionDisabled(value string) bool {
	switch strings.ToLower(value) {
	case "0", "false", "off":
		return true
	}
	return false
}
