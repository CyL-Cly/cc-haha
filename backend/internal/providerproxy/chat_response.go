package providerproxy

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func chatResponseFailure(message json.RawMessage) *ResponseError {
	failure := responseError(responseText(message))
	failure.MessageJSON = message
	return failure
}

// ChatResponseError preserves a model-level failure even on an HTTP success.
func ChatResponseError(raw json.RawMessage) error {
	value := requestObject(raw)
	if !value.IsObject() {
		return responseError("OpenAI Chat upstream returned an invalid response")
	}
	if failure := value.Get("error"); failure != nil && string(failure) != "null" {
		if requestIsString(failure) && requestTruthy(failure) {
			return chatResponseFailure(failure)
		}
		if message := requestObject(failure).Get("message"); requestIsString(message) && requestTruthy(message) {
			return chatResponseFailure(message)
		}
		return responseError("OpenAI Chat upstream reported an error")
	}
	if status := value.String("status"); status == "failed" || status == "cancelled" {
		return responseError("OpenAI Chat upstream response " + status)
	}
	return nil
}

func chatFinishReason(raw json.RawMessage) (string, error) {
	switch string(raw) {
	case `"stop"`, `"content_filter"`:
		return "end_turn", nil
	case `"tool_calls"`:
		return "tool_use", nil
	case `"length"`:
		return "max_tokens", nil
	}
	text, err := mediaInterpolation(requestEncode("OpenAI Chat upstream returned missing or unknown finish_reason: "), raw)
	if err != nil {
		return "", err
	}
	return "", chatResponseFailure(text)
}

func ParseCompleteChatToolArguments(value json.RawMessage) (json.RawMessage, error) {
	parsed := value
	if responseNullish(value) || string(value) == `""` {
		parsed = []byte(`{}`)
	}
	if requestIsString(value) && string(value) != `""` {
		units := responseUnits(value)
		var err error
		parsed, err = protocol.ParseAndStringifyJavaScriptJSON([]byte(protocol.JavaScriptJSONSourceUTF16(units)))
		if err != nil {
			return protocol.CreateUnparsedToolInput(units), nil
		}
	}
	if !requestObject(parsed).IsObject() {
		return nil, responseError("OpenAI Chat completed tool arguments must be a JSON object")
	}
	return parsed, nil
}

// ChatToAnthropic retains completed malformed arguments for native validation;
// truncated or filtered tool calls never become executable input.
func ChatToAnthropic(response protocol.JSONObject, model string, now func() int64) (protocol.JSONObject, error) {
	if err := ChatResponseError(requestEncode(response)); err != nil {
		return protocol.JSONObject{}, err
	}
	choices := requestArray(response.Get("choices"))
	if len(choices) == 0 {
		return protocol.JSONObject{}, responseError("OpenAI Chat upstream response has no message choice")
	}
	choice := requestObject(choices[0])
	if !requestTruthy(choice.Get("message")) {
		return protocol.JSONObject{}, responseError("OpenAI Chat upstream response has no message choice")
	}
	stop, err := chatFinishReason(choice.Get("finish_reason"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	message := requestObject(choice.Get("message"))
	content := []json.RawMessage{}
	if reasoning := message.Get("reasoning_content"); requestIsString(reasoning) && requestTruthy(reasoning) {
		content = append(content, requestEncode(requestRecord(requestField("type", "thinking"), protocol.JSONField{Name: "thinking", Value: reasoning})))
	} else if reasoning := message.Get("reasoning"); requestIsString(reasoning) && requestTruthy(reasoning) {
		content = append(content, requestEncode(requestRecord(requestField("type", "thinking"), protocol.JSONField{Name: "thinking", Value: reasoning})))
	} else if requestIsArray(message.Get("thinking_blocks")) {
		for _, raw := range requestArray(message.Get("thinking_blocks")) {
			if err := mediaPropertyError(raw, "tb.type"); err != nil {
				return protocol.JSONObject{}, err
			}
			block := requestObject(raw)
			if block.String("type") == "thinking" && requestIsString(block.Get("thinking")) {
				content = append(content, requestEncode(requestRecord(requestField("type", "thinking"), protocol.JSONField{Name: "thinking", Value: block.Get("thinking")}, protocol.JSONField{Name: "signature", Value: block.Get("signature")})))
			}
		}
	}
	if requestTruthy(message.Get("content")) {
		content = append(content, requestEncode(requestRecord(requestField("type", "text"), protocol.JSONField{Name: "text", Value: message.Get("content")})))
	}
	seen := map[string]bool{}
	for _, raw := range requestArray(message.Get("tool_calls")) {
		if stop == "max_tokens" || choice.String("finish_reason") == "content_filter" {
			continue
		}
		call := requestObject(raw)
		fn := requestObject(call.Get("function"))
		id := call.Get("id")
		if protocol.TrimJavaScriptString(call.String("id")) == "" || protocol.TrimJavaScriptString(fn.String("name")) == "" || seen[string(protocol.StringifyValidatedJavaScriptJSON(id))] {
			return protocol.JSONObject{}, responseError("OpenAI Chat tool call has missing or duplicate identity")
		}
		seen[string(protocol.StringifyValidatedJavaScriptJSON(id))] = true
		input, err := ParseCompleteChatToolArguments(fn.Get("arguments"))
		if err != nil {
			return protocol.JSONObject{}, responseError("OpenAI Chat completed tool arguments must be a JSON object")
		}
		content = append(content, requestEncode(requestRecord(requestField("type", "tool_use"), protocol.JSONField{Name: "id", Value: id}, protocol.JSONField{Name: "name", Value: fn.Get("name")}, protocol.JSONField{Name: "input", Value: input})))
	}
	if stop == "tool_use" && len(seen) == 0 {
		return protocol.JSONObject{}, responseError("OpenAI Chat upstream finished with tool_calls but supplied no tool calls")
	}
	if len(content) == 0 {
		content = append(content, requestEncode(requestRecord(requestField("type", "text"), requestField("text", ""))))
	}
	id := response.Get("id")
	if !requestTruthy(id) {
		id = responseFallbackID(now)
	}
	wireModel := response.Get("model")
	if !requestTruthy(wireModel) {
		wireModel = requestEncode(model)
	}
	return requestRecord(protocol.JSONField{Name: "id", Value: id}, requestField("type", "message"), requestField("role", "assistant"), requestField("content", content), protocol.JSONField{Name: "model", Value: wireModel}, requestField("stop_reason", stop), requestField("stop_sequence", nil), requestField("usage", OpenAIUsageToAnthropic(response.Get("usage")))), nil
}
