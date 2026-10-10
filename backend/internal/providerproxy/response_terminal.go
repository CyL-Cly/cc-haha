package providerproxy

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var responseStatusPrefix = regexp.MustCompile(`^\d{3}\s+`)
var responseOverload = regexp.MustCompile(`rate_limit|capacity|overload`)

func responsePolicy(value json.RawMessage, depth int) *ResponseError {
	if depth > 12 {
		return nil
	}
	record := responseObject(value)
	if !record.IsObject() {
		return nil
	}
	code := record.String("code")
	if code == "cyber_policy" || code == "content_policy" || code == "content_policy_violation" {
		message := record.Get("message")
		if !responseString(message) || protocol.TrimJavaScriptString(responseText(message)) == "" {
			message = responseJSON("Request rejected by the upstream safety policy.")
		}
		return &ResponseError{Message: responseText(message), MessageJSON: message, Code: code, Type: "permission_error", Status: 403}
	}
	for _, key := range []string{"error", "response", "originalError", "cause"} {
		if found := responsePolicy(record.Get(key), depth+1); found != nil {
			return found
		}
	}
	if responseString(record.Get("message")) {
		serialized := protocol.TrimJavaScriptString(responseStatusPrefix.ReplaceAllString(record.String("message"), ""))
		if strings.HasPrefix(serialized, "{") {
			parsed, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(serialized))
			if err == nil {
				return responsePolicy(parsed, depth+1)
			}
		}
	}
	return nil
}

func responsesTerminal(response protocol.JSONObject, event string) (string, error) {
	if policy := responsePolicy(responseJSON(response), 0); policy != nil {
		return "", policy
	}
	status := ""
	missing := false
	if responseString(response.Get("status")) {
		status = response.String("status")
	} else if event != "" {
		status = strings.TrimPrefix(event, "response.")
	} else {
		missing = true
	}
	embedded := responseObject(response.Get("error"))
	failedEvent := event == "response.failed" || event == "response.cancelled" || event == "error"
	if embedded.IsObject() || responseTruthy(response.Get("error")) || failedEvent || status == "failed" || status == "cancelled" {
		message := embedded.Get("message")
		if !responseString(message) {
			message = response.Get("message")
		}
		if !responseString(message) {
			message = response.Get("error")
		}
		if !responseString(message) {
			last := event
			if event == "" {
				last = "response." + status
				if missing {
					last = "response.undefined"
				}
			}
			message = responseJSON("OpenAI stream ended with " + last)
		}
		code := embedded.String("code")
		if !responseString(embedded.Get("code")) {
			code = response.String("code")
		}
		kind := "api_error"
		if responseOverload.MatchString(code) || responseOverload.MatchString(embedded.String("type")) {
			kind = "overloaded_error"
		}
		return "", &ResponseError{Message: responseText(message), MessageJSON: message, Type: kind}
	}
	if status == "completed" {
		return "completed", nil
	}
	if status == "incomplete" {
		reason := responseObject(response.Get("incomplete_details")).Get("reason")
		if responseString(reason) && (responseText(reason) == "max_output_tokens" || responseText(reason) == "max_tokens") {
			return "max_tokens", nil
		}
		cause := "unknown"
		if responseString(reason) {
			cause = responseText(reason)
		}
		return "", &ResponseError{Message: "OpenAI response was incomplete: " + cause, MessageJSON: responseConcat(responseJSON("OpenAI response was incomplete: "), responseJSON(cause)), Type: "api_error"}
	}
	if missing {
		status = "missing"
	}
	return "", &ResponseError{Message: "OpenAI response has no valid terminal status: " + status, MessageJSON: responseJSON("OpenAI response has no valid terminal status: " + status), Type: "api_error"}
}

func ParseResponsesToolArguments(value json.RawMessage) (json.RawMessage, error) {
	parsed := value
	if responseString(value) {
		units := responseUnits(value)
		start, end := 0, len(units)
		for start < end && protocol.IsJavaScriptWhitespace(rune(units[start])) {
			start++
		}
		for end > start && protocol.IsJavaScriptWhitespace(rune(units[end-1])) {
			end--
		}
		if start != end {
			source := []byte(protocol.JavaScriptJSONSourceUTF16(units))
			result, err := protocol.ParseAndStringifyJavaScriptJSON(source)
			if err != nil {
				return protocol.CreateUnparsedToolInput(units), nil
			}
			parsed = result
		}
	}
	if !responseObject(parsed).IsObject() {
		return nil, responseError("Invalid OpenAI Responses tool arguments: expected a JSON object")
	}
	return parsed, nil
}
func stringifyResponsesToolArguments(value json.RawMessage) json.RawMessage {
	if responseNullish(value) || string(value) == `""` {
		return responseJSON("")
	}
	if responseString(value) {
		return value
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(value)
	if err != nil {
		return responseJSON("")
	}
	return responseJSON(string(normalized))
}
