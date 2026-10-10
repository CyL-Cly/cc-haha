package providerproxy

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AnthropicToChat implements the saved-provider Chat request producer. Native
// runtime admission remains owned by the fully bound proxy service.
func AnthropicToChat(body, options protocol.JSONObject) (protocol.JSONObject, error) {
	compatOptions := options.Clone()
	compatOptions.Set("protocol", requestEncode("openai_chat"))
	compat, err := ResolveRequestCompatibility(body, compatOptions)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	messages := []json.RawMessage{}
	if requestTruthy(body.Get("system")) {
		text := body.Get("system")
		if requestIsString(text) {
			text = StripLeadingBillingHeader(text)
		} else {
			parts := []json.RawMessage{}
			for _, raw := range requestArray(text) {
				part := StripLeadingBillingHeader(requestObject(raw).Get("text"))
				if requestTruthy(part) {
					parts = append(parts, part)
				}
			}
			text = requestJoin(parts, "\n")
		}
		if requestTruthy(text) {
			messages = append(messages, requestEncode(requestRecord(requestField("role", "system"), protocol.JSONField{Name: "content", Value: text})))
		}
	}
	messageOptions := options.Clone()
	if !requestTruthy(compat.Get("passReasoning")) {
		messageOptions.Set("roundTripReasoningContent", requestEncode(false))
	}
	for _, raw := range requestArray(body.Get("messages")) {
		parts, err := chatRequestMessage(raw, messageOptions)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		messages = append(messages, parts...)
	}
	stream := string(body.Get("stream")) == "true"
	result := requestRecord(protocol.JSONField{Name: "model", Value: body.Get("model")}, requestField("messages", messages), requestField("stream", stream))
	if stream {
		result.Set("stream_options", requestEncode(requestRecord(requestField("include_usage", true))))
	}
	budget := requestObject(compat.Get("outputBudget"))
	if field := budget.String("field"); field == "max_tokens" || field == "max_completion_tokens" {
		result.Set(field, budget.Get("effective"))
	}
	if requestTruthy(compat.Get("passSamplingParams")) {
		result.Set("temperature", body.Get("temperature"))
		result.Set("top_p", body.Get("top_p"))
	}
	if len(requestArray(body.Get("stop_sequences"))) > 0 {
		result.Set("stop", body.Get("stop_sequences"))
	}
	tools := []json.RawMessage{}
	for _, raw := range requestArray(body.Get("tools")) {
		tool := requestObject(raw)
		if tool.String("name") == "BatchTool" {
			continue
		}
		fn := requestRecord(protocol.JSONField{Name: "name", Value: tool.Get("name")}, protocol.JSONField{Name: "description", Value: tool.Get("description")}, protocol.JSONField{Name: "parameters", Value: tool.Get("input_schema")})
		tools = append(tools, requestEncode(requestRecord(requestField("type", "function"), requestField("function", fn))))
	}
	if len(tools) > 0 {
		result.Set("tools", requestEncode(tools))
	}
	if body.Get("tool_choice") != nil {
		choice := chatToolChoice(body.Get("tool_choice"))
		name := requestObject(requestObject(choice).Get("function")).Get("name")
		selectable := !requestTruthy(name)
		for _, raw := range tools {
			if string(requestObject(requestObject(raw).Get("function")).Get("name")) == string(name) {
				selectable = true
			}
		}
		if len(tools) > 0 && selectable {
			result.Set("tool_choice", choice)
		}
	}
	if requestTruthy(compat.Get("passReasoning")) && requestTruthy(body.Get("thinking")) {
		thinking := requestObject(body.Get("thinking"))
		if raw := thinking.Get("budget_tokens"); raw != nil {
			effort := "high"
			if requestNumber(raw) <= 1024 {
				effort = "low"
			} else if requestNumber(raw) <= 8192 {
				effort = "medium"
			}
			result.Set("reasoning_effort", requestEncode(effort))
		} else if thinking.String("type") == "enabled" {
			result.Set("reasoning_effort", requestEncode("high"))
		}
		if requestTruthy(options.Get("passThinkingToggle")) {
			result.Set("thinking", requestEncode(requestRecord(protocol.JSONField{Name: "type", Value: thinking.Get("type")})))
		}
	}
	if effort := requestObject(body.Get("output_config")).Get("effort"); requestTruthy(compat.Get("passReasoning")) && requestReasoningEffort(effort) {
		result.Set("reasoning_effort", effort)
	}
	if compat.Get("parallelToolCalls") != nil && len(tools) > 0 {
		result.Set("parallel_tool_calls", compat.Get("parallelToolCalls"))
	}
	if raw := compat.Get("structuredOutput"); raw != nil {
		format := requestObject(raw)
		schema := format.Clone()
		schema.Delete("type")
		result.Set("response_format", requestEncode(requestRecord(protocol.JSONField{Name: "type", Value: format.Get("type")}, requestField("json_schema", schema))))
	}
	return result, nil
}

func chatToolChoice(raw json.RawMessage) json.RawMessage {
	choice := requestToolChoice(raw)
	if value := requestObject(choice); value.IsObject() && value.Get("name") != nil {
		return requestEncode(requestRecord(requestField("type", "function"), requestField("function", requestRecord(protocol.JSONField{Name: "name", Value: value.Get("name")}))))
	}
	return choice
}

func chatRequestMessage(raw json.RawMessage, options protocol.JSONObject) ([]json.RawMessage, error) {
	if err := mediaPropertyError(raw, "msg.content"); err != nil {
		return nil, err
	}
	message := requestObject(raw)
	content := message.Get("content")
	if requestIsString(content) {
		return []json.RawMessage{requestEncode(requestRecord(protocol.JSONField{Name: "role", Value: message.Get("role")}, protocol.JSONField{Name: "content", Value: content}))}, nil
	}
	if !requestIsArray(content) || len(requestArray(content)) == 0 {
		return []json.RawMessage{requestEncode(requestRecord(protocol.JSONField{Name: "role", Value: message.Get("role")}, requestField("content", "")))}, nil
	}
	if message.String("role") == "user" {
		return chatUserMessage(requestArray(content), options.String("imageContentMode"))
	}
	text, reasoning := requestEncode(""), requestEncode("")
	calls := []json.RawMessage{}
	for _, raw := range requestArray(content) {
		if err := mediaPropertyError(raw, "block.type"); err != nil {
			return nil, err
		}
		block := requestObject(raw)
		switch block.String("type") {
		case "text":
			var err error
			text, err = mediaInterpolation(text, block.Get("text"))
			if err != nil {
				return nil, err
			}
		case "thinking":
			if requestTruthy(options.Get("roundTripReasoningContent")) {
				var err error
				reasoning, err = mediaInterpolation(reasoning, block.Get("thinking"))
				if err != nil {
					return nil, err
				}
			}
		case "tool_use":
			arguments := block.Get("input")
			if arguments != nil && !requestIsString(arguments) {
				arguments = requestEncode(string(protocol.StringifyValidatedJavaScriptJSON(arguments)))
			}
			calls = append(calls, requestEncode(requestRecord(protocol.JSONField{Name: "id", Value: block.Get("id")}, requestField("type", "function"), requestField("function", requestRecord(protocol.JSONField{Name: "name", Value: block.Get("name")}, protocol.JSONField{Name: "arguments", Value: arguments})))))
		}
	}
	if !requestTruthy(text) {
		text = []byte("null")
	}
	output := requestRecord(requestField("role", "assistant"), protocol.JSONField{Name: "content", Value: text})
	if len(calls) > 0 {
		output.Set("tool_calls", requestEncode(calls))
	}
	if requestTruthy(reasoning) {
		output.Set("reasoning_content", reasoning)
	}
	return []json.RawMessage{requestEncode(output)}, nil
}
