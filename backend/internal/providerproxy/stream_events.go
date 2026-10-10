package providerproxy

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (p *ResponsesProcessor) processEvent(event string, data protocol.JSONObject) (bool, error) {
	switch event {
	case "response.created":
		response := responseObject(data.Get("response"))
		if !response.IsObject() {
			response = data
		}
		if responseTruthy(response.Get("model")) {
			p.model = response.Get("model")
		}
		if responseString(response.Get("id")) {
			p.messageID = response.Get("id")
		}
		p.messageStart()
	case "response.output_item.added":
		p.messageStart()
		item := responseObject(data.Get("item"))
		if !item.IsObject() {
			break
		}
		if item.String("type") == "function_call" {
			index, err := p.ensureTool(data, item)
			if err != nil {
				return false, err
			}
			if value := item.Get("arguments"); value != nil && string(value) != `""` {
				if err = p.reconcileTool(index, value); err != nil {
					return false, err
				}
			}
		} else if item.String("type") == "reasoning" && !p.options.OpenAICodexOAuth {
			p.ensureReasoning(data)
		}
	case "response.output_item.done":
		item := responseObject(data.Get("item"))
		if !item.IsObject() {
			break
		}
		switch item.String("type") {
		case "function_call":
			index, err := p.ensureTool(data, item)
			if err != nil {
				return false, err
			}
			if value := item.Get("arguments"); value != nil && string(value) != `""` {
				if err = p.reconcileTool(index, value); err != nil {
					return false, err
				}
			}
		case "message":
			if !responseArray(item.Get("content")) {
				break
			}
			var values []json.RawMessage
			_ = json.Unmarshal(item.Get("content"), &values)
			for i, value := range values {
				part := responseObject(value)
				if !part.IsObject() || !responsePartKind(part.Get("type")) {
					continue
				}
				partData := data.Clone()
				partData.Set("content_index", responseJSON(i))
				index := p.ensureText(partData)
				text := part.Get("text")
				if part.String("type") == "refusal" {
					text = part.Get("refusal")
				}
				if responseString(text) {
					if err := p.reconcileText(index, text); err != nil {
						return false, err
					}
				}
				p.closeBlock(index)
			}
		case "reasoning":
			key := responseKey(data.Get("output_index"))
			if p.reasoningDone[key] {
				break
			}
			if !p.options.OpenAICodexOAuth {
				if _, exists := p.mapReasoning[key]; !exists {
					texts := []json.RawMessage{}
					var rows []json.RawMessage
					if responseArray(item.Get("summary")) {
						_ = json.Unmarshal(item.Get("summary"), &rows)
					}
					for _, value := range rows {
						text := responseObject(value).Get("text")
						if responseString(text) {
							texts = append(texts, text)
						}
					}
					text, _ := protocol.JoinJavaScriptValuesJSON(texts, "")
					if responseTruthy(text) {
						index := p.ensureReasoning(data)
						p.delta(index, "thinking_delta", "thinking", text)
					}
				}
				p.closeReasoning(data)
				p.reasoningDone[key] = true
				break
			}
			encoded := encodeResponsesReasoning(item)
			summary := item.Get("summary")
			texts := []json.RawMessage{}
			if !responseNullish(summary) {
				if !responseArray(summary) {
					failure := responseError("reasoning.summary?.map is not a function")
					failure.Name = "TypeError"
					return false, failure
				}
				var parts []json.RawMessage
				_ = json.Unmarshal(summary, &parts)
				for _, part := range parts {
					if string(part) == "null" {
						failure := responseError("null is not an object (evaluating 'part.text')")
						failure.Name = "TypeError"
						return false, failure
					}
					texts = append(texts, responseObject(part).Get("text"))
				}
			}
			text, _ := protocol.JoinJavaScriptValuesJSON(texts, "")
			if !responseTruthy(encoded) && !responseTruthy(text) {
				break
			}
			p.messageStart()
			index := p.next
			p.next++
			if encoded != nil {
				p.startBlock(index, responseRecord(responseField("type", "redacted_thinking"), responseRawField("data", encoded)))
			} else {
				p.startBlock(index, responseRecord(responseField("type", "thinking"), responseRawField("thinking", text)))
			}
			p.stopBlock(index)
			p.reasoningDone[key] = true
		}
	case "response.content_part.added":
		part := responseObject(data.Get("part"))
		if !part.IsObject() || !responsePartKind(part.Get("type")) {
			break
		}
		index := p.ensureText(data)
		initial := part.Get("text")
		if part.String("type") == "refusal" {
			initial = part.Get("refusal")
		}
		if responseString(initial) {
			if err := p.reconcileText(index, initial); err != nil {
				return false, err
			}
		}
	case "response.reasoning_summary_part.added":
		if !p.options.OpenAICodexOAuth {
			p.ensureReasoning(data)
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if p.options.OpenAICodexOAuth {
			break
		}
		index := p.ensureReasoning(data)
		delta := data.Get("delta")
		if !responseString(delta) || !responseTruthy(delta) {
			break
		}
		p.delta(index, "thinking_delta", "thinking", delta)
	case "response.output_text.delta", "response.refusal.delta":
		index := p.ensureText(data)
		if !p.open[index] {
			return false, responseError("OpenAI Responses text delta arrived after block completion")
		}
		delta := data.Get("delta")
		if !responseString(delta) {
			delta = responseJSON("")
		}
		if err := p.reconcileText(index, responseConcat(p.texts[index], delta)); err != nil {
			return false, err
		}
	case "response.function_call_arguments.delta":
		index, ok := p.toolIndex(data, false)
		if !ok {
			return false, responseError("OpenAI Responses tool arguments have no matching tool")
		}
		delta := stringifyResponsesToolArguments(data.Get("delta"))
		tool := p.tools[index]
		tool.arguments = responseConcat(tool.arguments, delta)
		p.delta(index, "input_json_delta", "partial_json", delta)
	case "response.output_text.done", "response.refusal.done":
		index := p.ensureText(data)
		text := data.Get("text")
		if event == "response.refusal.done" {
			text = data.Get("refusal")
		}
		if responseString(text) {
			if err := p.reconcileText(index, text); err != nil {
				return false, err
			}
		}
		p.closeBlock(index)
	case "response.function_call_arguments.done":
		index, ok := p.toolIndex(data, true)
		if !ok {
			return false, responseError("OpenAI Responses tool arguments have no matching tool")
		}
		value := data.Get("arguments")
		if responseNullish(value) {
			value = responseObject(data.Get("item")).Get("arguments")
		}
		if value != nil {
			if err := p.reconcileTool(index, value); err != nil {
				return false, err
			}
		}
	case "response.incomplete", "response.failed", "response.cancelled", "error", "response.completed":
		return p.terminalOutput(data, event)
	}
	return false, nil
}
