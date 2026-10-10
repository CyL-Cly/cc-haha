package providerproxy

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const openAIReasoningPrefix = "cc-haha:openai-reasoning:v1:"

func encodeResponsesReasoning(item protocol.JSONObject) json.RawMessage {
	if !responseTruthy(item.Get("encrypted_content")) {
		return nil
	}
	envelope := responseRecord()
	if responseTruthy(item.Get("id")) {
		envelope.Set("id", item.Get("id"))
	}
	summary := item.Get("summary")
	if responseNullish(summary) {
		summary = responseJSON([]any{})
	}
	envelope.Set("summary", summary)
	envelope.Set("encrypted_content", item.Get("encrypted_content"))
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(responseJSON(envelope))
	if err != nil {
		return nil
	}
	return responseConcat(responseJSON(openAIReasoningPrefix), responseJSON(string(normalized)))
}

// ResponsesToAnthropic ports the actual buffered response transform. Terminal
// errors and incomplete tools never become executable Anthropic tool calls.
func ResponsesToAnthropic(response protocol.JSONObject, model string, o ResponsesOptions) (protocol.JSONObject, error) {
	terminal, err := responsesTerminal(response, "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !responseArray(response.Get("output")) {
		return protocol.JSONObject{}, responseError("Invalid OpenAI Responses output: expected an array")
	}
	var items []json.RawMessage
	_ = json.Unmarshal(response.Get("output"), &items)
	content := []protocol.JSONObject{}
	hasTools := false
	for _, value := range items {
		if string(value) == "null" {
			failure := responseError("null is not an object (evaluating 'item.type')")
			failure.Name = "TypeError"
			return protocol.JSONObject{}, failure
		}
		item := responseObject(value)
		if item.String("type") == "function_call" && terminal == "max_tokens" {
			continue
		}
		switch item.String("type") {
		case "message":
			parts := item.Get("content")
			if !responseTruthy(parts) {
				continue
			}
			if !responseArray(parts) {
				if responseString(parts) {
					continue
				}
				failure := responseError("{} is not iterable")
				failure.Name = "TypeError"
				if len(parts) > 0 && parts[0] != '{' {
					failure = responseError(string(parts) + " is not iterable")
					failure.Name = "TypeError"
				}
				return protocol.JSONObject{}, failure
			}
			var rows []json.RawMessage
			_ = json.Unmarshal(parts, &rows)
			for _, value := range rows {
				if string(value) == "null" {
					failure := responseError("null is not an object (evaluating 'part.type')")
					failure.Name = "TypeError"
					return protocol.JSONObject{}, failure
				}
				part := responseObject(value)
				text := part.Get("text")
				switch part.String("type") {
				case "output_text", "text":
					if !responseTruthy(text) {
						text = responseJSON("")
					}
					content = append(content, responseRecord(responseField("type", "text"), responseRawField("text", text)))
				case "refusal":
					text = part.Get("refusal")
					if !responseTruthy(text) {
						text = responseJSON("[Refusal]")
					}
					content = append(content, responseRecord(responseField("type", "text"), responseRawField("text", text)))
				}
			}
		case "function_call":
			if !responseTruthy(item.Get("call_id")) || !responseTruthy(item.Get("name")) {
				return protocol.JSONObject{}, responseError("Invalid OpenAI Responses tool identity")
			}
			input, err := ParseResponsesToolArguments(item.Get("arguments"))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			content = append(content, responseRecord(responseField("type", "tool_use"), responseRawField("id", item.Get("call_id")), responseRawField("name", item.Get("name")), responseRawField("input", input)))
			hasTools = true
		case "reasoning":
			if o.PreserveOpenAIReasoning {
				if data := encodeResponsesReasoning(item); data != nil {
					content = append(content, responseRecord(responseField("type", "redacted_thinking"), responseRawField("data", data)))
					continue
				}
			}
			summary := item.Get("summary")
			if !responseTruthy(summary) {
				continue
			}
			if !responseArray(summary) {
				if responseString(summary) {
					continue
				}
				failure := responseError("{} is not iterable")
				failure.Name = "TypeError"
				if len(summary) > 0 && summary[0] != '{' {
					failure = responseError(string(summary) + " is not iterable")
					failure.Name = "TypeError"
				}
				return protocol.JSONObject{}, failure
			}
			var rows []json.RawMessage
			_ = json.Unmarshal(summary, &rows)
			for _, value := range rows {
				if string(value) == "null" {
					failure := responseError("null is not an object (evaluating 's.text')")
					failure.Name = "TypeError"
					return protocol.JSONObject{}, failure
				}
				part := responseObject(value)
				if responseTruthy(part.Get("text")) {
					content = append(content, responseRecord(responseField("type", "thinking"), responseRawField("thinking", part.Get("text"))))
				}
			}
		}
	}
	if len(content) == 0 {
		content = append(content, responseRecord(responseField("type", "text"), responseField("text", "")))
	}
	id := response.Get("id")
	if !responseTruthy(id) {
		id = responseFallbackID(o.NowMillis)
	}
	used := response.Get("model")
	if !responseTruthy(used) {
		used = responseJSON(model)
	}
	stop := "end_turn"
	if terminal == "max_tokens" {
		stop = "max_tokens"
	} else if hasTools {
		stop = "tool_use"
	}
	return responseRecord(responseRawField("id", id), responseField("type", "message"), responseField("role", "assistant"), responseField("content", content), responseRawField("model", used), responseField("stop_reason", stop), responseField("stop_sequence", nil), responseField("usage", OpenAIUsageToAnthropic(response.Get("usage")))), nil
}
