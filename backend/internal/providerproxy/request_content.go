package providerproxy

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func StripLeadingBillingHeader(text json.RawMessage) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return text
	}
	const prefix = "x-anthropic-billing-header:"
	if len(units) < len(prefix) {
		return text
	}
	for i := range prefix {
		if units[i] != uint16(prefix[i]) {
			return text
		}
	}
	lineEnd := -1
	for i, unit := range units {
		if unit == '\r' || unit == '\n' {
			lineEnd = i
			break
		}
	}
	if lineEnd < 0 {
		return requestEncode("")
	}
	start := lineEnd + 1
	if units[lineEnd] == '\r' && start < len(units) && units[start] == '\n' {
		start++
	}
	if start < len(units) && (units[start] == '\r' || units[start] == '\n') {
		if units[start] == '\r' && start+1 < len(units) && units[start+1] == '\n' {
			start += 2
		} else {
			start++
		}
	}
	return protocol.QuoteJavaScriptUTF16(units[start:])
}

func requestDocumentProvenance(block protocol.JSONObject) json.RawMessage {
	lines := []json.RawMessage{}
	if requestTruthy(block.Get("title")) {
		lines = append(lines, requestJoin([]json.RawMessage{requestEncode("[Document: "), requestString(block.Get("title")), requestEncode("]")}, ""))
	}
	if requestTruthy(block.Get("context")) {
		lines = append(lines, requestJoin([]json.RawMessage{requestEncode("[Document context: "), requestString(block.Get("context")), requestEncode("]")}, ""))
	}
	if len(lines) == 0 {
		return requestEncode("")
	}
	return requestJoin([]json.RawMessage{requestJoin(lines, "\n"), requestEncode("\n")}, "")
}
func requestInputText(text json.RawMessage) json.RawMessage {
	return requestEncode(requestRecord(requestField("type", "input_text"), protocol.JSONField{Name: "text", Value: text}))
}

func requestContentBlock(block protocol.JSONObject) []json.RawMessage {
	kind := block.String("type")
	if kind == "text" {
		return []json.RawMessage{requestInputText(block.Get("text"))}
	}
	if kind == "search_result" {
		parts := []json.RawMessage{block.Get("title")}
		for _, raw := range requestArray(block.Get("content")) {
			part := requestObject(raw)
			if part.String("type") == "text" {
				parts = append(parts, part.Get("text"))
			}
		}
		parts = append(parts, block.Get("source"))
		filtered := []json.RawMessage{}
		for _, part := range parts {
			if requestIsString(part) && requestTruthy(part) {
				filtered = append(filtered, part)
			}
		}
		return []json.RawMessage{requestInputText(requestJoin(filtered, " — "))}
	}
	source := requestObject(block.Get("source"))
	if kind == "document" {
		provenance := requestDocumentProvenance(block)
		switch source.String("type") {
		case "text":
			return []json.RawMessage{requestInputText(requestJoin([]json.RawMessage{provenance, requestString(source.Get("data"))}, ""))}
		case "content":
			parts := []json.RawMessage{}
			if requestTruthy(provenance) {
				parts = append(parts, requestInputText(provenance))
			}
			if requestIsString(source.Get("content")) {
				return append(parts, requestInputText(source.Get("content")))
			}
			for _, raw := range requestArray(source.Get("content")) {
				parts = append(parts, requestContentBlock(requestObject(raw))...)
			}
			return parts
		case "url":
			parts := []json.RawMessage{}
			if requestTruthy(provenance) {
				parts = append(parts, requestInputText(provenance))
			}
			file := requestRecord(requestField("type", "input_file"), protocol.JSONField{Name: "file_url", Value: source.Get("url")})
			if requestTruthy(block.Get("title")) {
				file.Set("filename", block.Get("title"))
			}
			return append(parts, requestEncode(file))
		case "file":
			prefix := requestEncode("")
			if requestTruthy(block.Get("context")) {
				prefix = requestJoin([]json.RawMessage{requestEncode("[Document context: "), requestString(block.Get("context")), requestEncode("]\n")}, "")
			}
			text := requestJoin([]json.RawMessage{prefix, requestEncode("[Document: "), requestString(requestFallback(block.Get("title"), requestEncode("file"))), requestEncode(" omitted — file-based source]")}, "")
			return []json.RawMessage{requestInputText(text)}
		default:
			parts := []json.RawMessage{}
			if requestTruthy(provenance) {
				parts = append(parts, requestInputText(provenance))
			}
			file := requestRecord(requestField("type", "input_file"), protocol.JSONField{Name: "file_data", Value: requestDataURL(source)})
			if requestTruthy(block.Get("title")) {
				file.Set("filename", block.Get("title"))
			}
			return append(parts, requestEncode(file))
		}
	}
	if source.String("type") == "file" {
		return []json.RawMessage{requestInputText(requestEncode("[Image omitted: file-based image source is not supported by this endpoint.]"))}
	}
	image := requestDataURL(source)
	if source.String("type") == "url" {
		image = source.Get("url")
	}
	return []json.RawMessage{requestEncode(requestRecord(requestField("type", "input_image"), protocol.JSONField{Name: "image_url", Value: image}))}
}

func requestDataURL(source protocol.JSONObject) json.RawMessage {
	return requestJoin([]json.RawMessage{requestEncode("data:"), requestString(source.Get("media_type")), requestEncode(";base64,"), requestString(source.Get("data"))}, "")
}

func DecodeOpenAIReasoningEnvelope(data json.RawMessage) (protocol.JSONObject, bool) {
	const prefix = "cc-haha:openai-reasoning:v1:"
	units, err := protocol.DecodeJavaScriptJSONString(data)
	if err != nil || len(units) < len(prefix) {
		return protocol.JSONObject{}, false
	}
	for i := range prefix {
		if units[i] != uint16(prefix[i]) {
			return protocol.JSONObject{}, false
		}
	}
	source := protocol.JavaScriptJSONSourceUTF16(units[len(prefix):])
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(source))
	if err != nil {
		return protocol.JSONObject{}, false
	}
	envelope := requestObject(normalized)
	if !envelope.IsObject() || !requestIsString(envelope.Get("encrypted_content")) || !requestTruthy(envelope.Get("encrypted_content")) || envelope.Get("id") != nil && !requestIsString(envelope.Get("id")) || !requestIsArray(envelope.Get("summary")) {
		return protocol.JSONObject{}, false
	}
	for _, raw := range requestArray(envelope.Get("summary")) {
		part := requestObject(raw)
		if !part.IsObject() || !requestIsString(part.Get("type")) || !requestIsString(part.Get("text")) {
			return protocol.JSONObject{}, false
		}
	}
	result := requestRecord(requestField("type", "reasoning"))
	if requestTruthy(envelope.Get("id")) {
		result.Set("id", envelope.Get("id"))
	}
	result.Set("summary", envelope.Get("summary"))
	result.Set("encrypted_content", envelope.Get("encrypted_content"))
	return result, true
}

func requestMessage(msg protocol.JSONObject, preserveReasoning bool) []json.RawMessage {
	role := msg.Get("role")
	content := msg.Get("content")
	message := func(value json.RawMessage) json.RawMessage {
		return requestEncode(requestRecord(requestField("type", "message"), protocol.JSONField{Name: "role", Value: role}, protocol.JSONField{Name: "content", Value: value}))
	}
	if requestIsString(content) {
		return []json.RawMessage{message(content)}
	}
	blocks := requestArray(content)
	if len(blocks) == 0 {
		return []json.RawMessage{message(requestEncode(""))}
	}
	items := []json.RawMessage{}
	parts := []json.RawMessage{}
	flush := func() {
		if len(parts) == 0 {
			return
		}
		texts := []json.RawMessage{}
		allText := true
		for _, raw := range parts {
			part := requestObject(raw)
			if part.String("type") != "input_text" {
				allText = false
			}
			texts = append(texts, part.Get("text"))
		}
		if allText {
			text := requestJoin(texts, "")
			if requestTruthy(text) {
				items = append(items, message(text))
			}
		} else {
			items = append(items, message(requestEncode(parts)))
		}
		parts = nil
	}
	for _, raw := range blocks {
		block := requestObject(raw)
		switch block.String("type") {
		case "text", "image", "document", "search_result":
			parts = append(parts, requestContentBlock(block)...)
		case "tool_use":
			flush()
			arguments := block.Get("input")
			if !requestIsString(arguments) && arguments != nil {
				arguments = requestEncode(string(protocol.StringifyValidatedJavaScriptJSON(arguments)))
			}
			items = append(items, requestEncode(requestRecord(requestField("type", "function_call"), protocol.JSONField{Name: "call_id", Value: block.Get("id")}, protocol.JSONField{Name: "name", Value: block.Get("name")}, protocol.JSONField{Name: "arguments", Value: arguments})))
		case "tool_result":
			flush()
			output := block.Get("content")
			if !requestIsString(output) {
				converted := []json.RawMessage{}
				allText := true
				for _, raw := range requestArray(output) {
					part := requestObject(raw)
					if part.String("type") != "text" {
						allText = false
					}
					if strings.Contains("|text|image|document|search_result|", "|"+part.String("type")+"|") {
						converted = append(converted, requestContentBlock(part)...)
					}
				}
				if allText {
					texts := []json.RawMessage{}
					for _, raw := range converted {
						texts = append(texts, requestObject(raw).Get("text"))
					}
					output = requestJoin(texts, "")
				} else {
					output = requestEncode(converted)
				}
			}
			items = append(items, requestEncode(requestRecord(requestField("type", "function_call_output"), protocol.JSONField{Name: "call_id", Value: block.Get("tool_use_id")}, protocol.JSONField{Name: "output", Value: output})))
		case "redacted_thinking":
			if preserveReasoning {
				flush()
				if reasoning, ok := DecodeOpenAIReasoningEnvelope(block.Get("data")); ok {
					items = append(items, requestEncode(reasoning))
				}
			}
		}
	}
	flush()
	return items
}
