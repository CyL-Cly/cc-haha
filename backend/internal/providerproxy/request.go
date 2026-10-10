package providerproxy

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AnthropicToResponses is the original complete request producer. It does not
// select credentials, start a model loop, or grant unsupported runtime access.
func AnthropicToResponses(body, options protocol.JSONObject) (protocol.JSONObject, error) {
	compatOptions := options.Clone()
	compatOptions.Set("protocol", requestEncode("openai_responses"))
	compat, err := ResolveRequestCompatibility(body, compatOptions)
	if err != nil {
		return requestRecord(), err
	}
	input := []json.RawMessage{}
	for _, raw := range requestArray(body.Get("messages")) {
		input = append(input, requestMessage(requestObject(raw), requestTruthy(options.Get("preserveOpenAIReasoning")))...)
	}
	result := requestRecord(protocol.JSONField{Name: "model", Value: body.Get("model")}, requestField("input", input), protocol.JSONField{Name: "stream", Value: body.Get("stream")}, requestField("store", false))
	if requestTruthy(body.Get("system")) {
		instructions := body.Get("system")
		if requestIsString(instructions) {
			instructions = StripLeadingBillingHeader(instructions)
		} else {
			parts := []json.RawMessage{}
			for _, raw := range requestArray(instructions) {
				text := StripLeadingBillingHeader(requestObject(raw).Get("text"))
				if requestTruthy(text) {
					parts = append(parts, text)
				}
			}
			instructions = requestJoin(parts, "\n")
		}
		if requestTruthy(instructions) {
			result.Set("instructions", instructions)
		}
	}
	if requestTruthy(options.Get("cacheKey")) {
		result.Set("prompt_cache_key", options.Get("cacheKey"))
	}
	budget := requestObject(compat.Get("outputBudget"))
	if budget.String("field") == "max_output_tokens" {
		result.Set("max_output_tokens", budget.Get("effective"))
	}
	if requestTruthy(compat.Get("passSamplingParams")) {
		result.Set("temperature", body.Get("temperature"))
		result.Set("top_p", body.Get("top_p"))
	}
	tools := []json.RawMessage{}
	for _, raw := range requestArray(body.Get("tools")) {
		tool := requestObject(raw)
		if tool.String("name") == "BatchTool" {
			continue
		}
		tools = append(tools, requestEncode(requestRecord(requestField("type", "function"), protocol.JSONField{Name: "name", Value: tool.Get("name")}, protocol.JSONField{Name: "description", Value: tool.Get("description")}, protocol.JSONField{Name: "parameters", Value: tool.Get("input_schema")}, requestField("strict", false))))
	}
	if len(tools) > 0 {
		result.Set("tools", requestEncode(tools))
	}
	if body.Get("tool_choice") != nil {
		choice := requestToolChoice(body.Get("tool_choice"))
		selectable := len(tools) > 0
		if named := requestObject(choice); named.IsObject() && requestIsString(named.Get("name")) {
			selectable = false
			for _, raw := range tools {
				if string(requestObject(raw).Get("name")) == string(named.Get("name")) {
					selectable = true
				}
			}
		}
		if selectable {
			result.Set("tool_choice", choice)
		}
	}
	if requestTruthy(compat.Get("passReasoning")) {
		thinking := requestObject(body.Get("thinking"))
		if requestTruthy(body.Get("thinking")) {
			if budget := thinking.Get("budget_tokens"); budget != nil {
				effort := "high"
				if requestNumber(budget) <= 1024 {
					effort = "low"
				} else if requestNumber(budget) <= 8192 {
					effort = "medium"
				}
				result.Set("reasoning", requestEncode(requestRecord(requestField("effort", effort))))
			} else if thinking.String("type") == "enabled" {
				result.Set("reasoning", requestEncode(requestRecord(requestField("effort", "high"))))
			}
		}
		if effort := requestObject(body.Get("output_config")).Get("effort"); requestReasoningEffort(effort) {
			reasoning := requestObject(result.Get("reasoning")).Clone()
			reasoning.Set("effort", effort)
			result.Set("reasoning", requestEncode(reasoning))
		}
	}
	if compat.Get("parallelToolCalls") != nil && len(tools) > 0 {
		result.Set("parallel_tool_calls", compat.Get("parallelToolCalls"))
	}
	if format := compat.Get("structuredOutput"); format != nil {
		result.Set("text", requestEncode(requestRecord(protocol.JSONField{Name: "format", Value: format})))
	}
	return result, nil
}

func requestToolChoice(raw json.RawMessage) json.RawMessage {
	if requestIsString(raw) {
		if string(raw) == `"any"` {
			return requestEncode("required")
		}
		return raw
	}
	value := requestObject(raw)
	switch value.String("type") {
	case "auto", "none":
		return value.Get("type")
	case "any":
		return requestEncode("required")
	case "tool":
		if requestIsString(value.Get("name")) {
			return requestEncode(requestRecord(requestField("type", "function"), protocol.JSONField{Name: "name", Value: value.Get("name")}))
		}
	}
	return requestEncode("auto")
}
func requestReasoningEffort(raw json.RawMessage) bool {
	switch string(raw) {
	case `"none"`, `"minimal"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`:
		return true
	}
	return false
}
