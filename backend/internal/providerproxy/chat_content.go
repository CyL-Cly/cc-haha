package providerproxy

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const chatOmittedImage = "\n[Image omitted: this OpenAI-compatible chat endpoint only supports text content.]\n"
const chatFileImage = "\n[Image omitted: file-based image source is not supported by this endpoint.]\n"

func chatText(text json.RawMessage, synthetic bool) protocol.JSONObject {
	part := requestRecord(requestField("type", "text"), protocol.JSONField{Name: "text", Value: text})
	if synthetic {
		part.Set("synthetic", requestEncode(true))
	}
	return part
}
func chatNeedsBoundary(left, right json.RawMessage) bool {
	l, r := responseUnits(left), responseUnits(right)
	return (len(l) == 0 || l[len(l)-1] != '\n' && l[len(l)-1] != '\r') && (len(r) == 0 || r[0] != '\n' && r[0] != '\r')
}
func chatEndsLF(raw json.RawMessage) bool {
	units := responseUnits(raw)
	return len(units) > 0 && units[len(units)-1] == '\n'
}

func chatCreateUser(parts []protocol.JSONObject, mode string) (json.RawMessage, error) {
	texts := []protocol.JSONObject{}
	wire := []json.RawMessage{}
	allPlain := true
	for _, part := range parts {
		if part.String("type") == "text" {
			texts = append(texts, part)
			wire = append(wire, requestEncode(chatText(part.Get("text"), false)))
			if requestTruthy(part.Get("synthetic")) {
				allPlain = false
			}
		} else {
			wire = append(wire, requestEncode(part))
		}
	}
	content := requestEncode(wire)
	if mode == "text_only" {
		content = requestEncode("")
		for index, part := range texts {
			if index > 0 && (requestTruthy(texts[index-1].Get("synthetic")) || requestTruthy(part.Get("synthetic"))) && chatNeedsBoundary(content, part.Get("text")) {
				content = responseConcat(content, requestEncode("\n"))
			}
			var err error
			content, err = mediaInterpolation(content, part.Get("text"))
			if err != nil {
				return nil, err
			}
		}
	} else if len(texts) == len(parts) && (len(parts) == 1 || allPlain) {
		raw := []json.RawMessage{}
		for _, part := range texts {
			raw = append(raw, part.Get("text"))
		}
		content = requestJoin(raw, "")
	}
	return requestEncode(requestRecord(requestField("role", "user"), protocol.JSONField{Name: "content", Value: content})), nil
}

func chatImage(block protocol.JSONObject) (protocol.JSONObject, error) {
	if err := mediaPropertyError(block.Get("source"), "block.source.type"); err != nil {
		return protocol.JSONObject{}, err
	}
	source := requestObject(block.Get("source"))
	url := source.Get("url")
	if source.String("type") == "file" {
		return protocol.JSONObject{}, responseError("file-based image source cannot be converted to a URL")
	}
	if source.String("type") != "url" {
		var err error
		url, err = mediaInterpolation(requestEncode("data:"), source.Get("media_type"), requestEncode(";base64,"), source.Get("data"))
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	return requestRecord(requestField("type", "image_url"), requestField("image_url", requestRecord(protocol.JSONField{Name: "url", Value: url}))), nil
}

func chatDocumentParts(block protocol.JSONObject) ([]protocol.JSONObject, error) {
	source := requestObject(block.Get("source"))
	parts := []protocol.JSONObject{}
	provenance, err := mediaDocumentProvenance(block)
	if err != nil {
		return nil, err
	}
	if requestTruthy(provenance) {
		parts = append(parts, chatText(provenance, false))
	}
	if requestIsString(source.Get("content")) {
		return append(parts, chatText(source.Get("content"), false)), nil
	}
	for _, raw := range requestArray(source.Get("content")) {
		if err := mediaPropertyError(raw, "part.type"); err != nil {
			return nil, err
		}
		part := requestObject(raw)
		if part.String("type") == "text" {
			parts = append(parts, chatText(part.Get("text"), false))
			continue
		}
		if err := mediaPropertyError(part.Get("source"), "part.source.type"); err != nil {
			return nil, err
		}
		if requestObject(part.Get("source")).String("type") == "file" {
			parts = append(parts, chatText(requestEncode("\n[Image omitted from document content]\n"), false))
		} else {
			image, err := chatImage(part)
			if err != nil {
				return nil, err
			}
			parts = append(parts, image)
		}
	}
	return parts, nil
}

func chatDocumentReference(block protocol.JSONObject) (json.RawMessage, error) {
	source := requestObject(block.Get("source"))
	prefix := requestEncode("")
	if requestTruthy(block.Get("context")) {
		var err error
		prefix, err = mediaInterpolation(requestEncode("[Document context: "), block.Get("context"), requestEncode("]\n"))
		if err != nil {
			return nil, err
		}
	}
	switch source.String("type") {
	case "url":
		return mediaInterpolation(prefix, requestEncode("[Document: "), requestFallback(block.Get("title"), source.Get("url")), requestEncode("]("), source.Get("url"), requestEncode(")"))
	case "file":
		return mediaInterpolation(prefix, requestEncode("[Document: "), requestFallback(block.Get("title"), requestEncode("file")), requestEncode(" omitted — file-based source]"))
	case "content":
		return mediaInterpolation(prefix, requestEncode("[Document: "), requestFallback(block.Get("title"), requestEncode("document")), requestEncode("]"))
	case "base64":
		if strings.HasPrefix(source.String("media_type"), "text/") {
			clean := strings.Map(func(r rune) rune {
				switch {
				case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/':
					return r
				case r == '-':
					return '+'
				case r == '_':
					return '/'
				case r == '=':
					return r
				}
				return -1
			}, source.String("data"))
			if at := strings.IndexByte(clean, '='); at >= 0 {
				clean = clean[:at]
			}
			if len(clean)%4 == 1 {
				clean = clean[:len(clean)-1]
			}
			decoded, _ := base64.RawStdEncoding.DecodeString(clean)
			units := utf16.Encode([]rune(string(protocol.DecodeJavaScriptUTF8(decoded))))
			suffix := ""
			if len(units) > 2000 {
				units = units[:2000]
				suffix = "\n[Document content truncated]"
			}
			return mediaInterpolation(prefix, requestEncode("[Document: "), requestFallback(block.Get("title"), source.Get("media_type")), requestEncode("]\n"), protocol.QuoteJavaScriptUTF16(units), requestEncode(suffix))
		}
	}
	return mediaInterpolation(prefix, requestEncode("[Document: "), requestFallback(block.Get("title"), source.Get("media_type")), requestEncode("]"))
}

func chatSearchText(block protocol.JSONObject) json.RawMessage {
	parts := []json.RawMessage{block.Get("title")}
	for _, raw := range requestArray(block.Get("content")) {
		part := requestObject(raw)
		if part.String("type") == "text" {
			parts = append(parts, part.Get("text"))
		}
	}
	parts = append(parts, block.Get("source"))
	filtered := []json.RawMessage{}
	for _, raw := range parts {
		if requestIsString(raw) && requestTruthy(raw) {
			filtered = append(filtered, raw)
		}
	}
	return requestJoin(filtered, " — ")
}

func chatToolResult(block protocol.JSONObject, mode string) (json.RawMessage, []protocol.JSONObject, error) {
	content := block.Get("content")
	if err := mediaPropertyError(content, "content"); err != nil {
		return nil, nil, err
	}
	parts := requestArray(content)
	if requestIsString(content) {
		parts = []json.RawMessage{requestEncode(chatText(content, false))}
	}
	text := []json.RawMessage{}
	media := []protocol.JSONObject{}
	pending := false
	for _, raw := range parts {
		if err := mediaPropertyError(raw, "resultBlock.type"); err != nil {
			return nil, nil, err
		}
		part := requestObject(raw)
		switch part.String("type") {
		case "text":
			if pending {
				if chatNeedsBoundary(requestEncode(""), part.Get("text")) {
					text = append(text, requestEncode("\n"))
				}
				pending = false
			}
			text = append(text, part.Get("text"))
		case "search_result":
			value := chatSearchText(part)
			if requestTruthy(value) {
				if pending {
					if chatNeedsBoundary(requestEncode(""), value) {
						text = append(text, requestEncode("\n"))
					}
					pending = false
				} else if len(text) > 0 && !chatEndsLF(text[len(text)-1]) {
					text = append(text, requestEncode("\n"))
				}
				text = append(text, value)
				pending = true
			}
		case "image":
			if err := mediaPropertyError(part.Get("source"), "resultBlock.source.type"); err != nil {
				return nil, nil, err
			}
			if requestObject(part.Get("source")).String("type") == "file" {
				pending = false
				text = append(text, requestEncode(chatFileImage))
			} else if mode == "text_only" {
				pending = false
				text = append(text, requestEncode(chatOmittedImage))
			} else {
				image, err := chatImage(part)
				if err != nil {
					return nil, nil, err
				}
				media = append(media, image)
			}
		case "document":
			if err := mediaPropertyError(part.Get("source"), "resultBlock.source.type"); err != nil {
				return nil, nil, err
			}
			if len(text) > 0 && !chatEndsLF(text[len(text)-1]) {
				text = append(text, requestEncode("\n"))
			}
			source := requestObject(part.Get("source"))
			switch source.String("type") {
			case "text":
				prefix, err := mediaDocumentProvenance(part)
				if err != nil {
					return nil, nil, err
				}
				value, err := mediaInterpolation(prefix, source.Get("data"))
				if err != nil {
					return nil, nil, err
				}
				text = append(text, value)
			case "content":
				values, err := chatDocumentParts(part)
				if err != nil {
					return nil, nil, err
				}
				for _, value := range values {
					if value.String("type") == "image_url" {
						if mode == "text_only" {
							text = append(text, requestEncode(chatOmittedImage))
						} else {
							media = append(media, value)
						}
					} else {
						text = append(text, value.Get("text"))
					}
				}
			default:
				value, err := chatDocumentReference(part)
				if err != nil {
					return nil, nil, err
				}
				text = append(text, value)
			}
			pending = true
		}
	}
	result := requestJoin(text, "")
	if !requestTruthy(result) && len(media) > 0 {
		result = requestEncode("Media result attached after this tool result.")
	}
	return result, media, nil
}

func chatUserMessage(blocks []json.RawMessage, mode string) ([]json.RawMessage, error) {
	leading, trailing := []protocol.JSONObject{}, []protocol.JSONObject{}
	tools := []json.RawMessage{}
	type group struct {
		id    json.RawMessage
		parts []protocol.JSONObject
	}
	groups := []group{}
	sawTool := false
	appendPart := func(part protocol.JSONObject) {
		if sawTool {
			trailing = append(trailing, part)
		} else {
			leading = append(leading, part)
		}
	}
	for _, raw := range blocks {
		if err := mediaPropertyError(raw, "block.type"); err != nil {
			return nil, err
		}
		block := requestObject(raw)
		switch block.String("type") {
		case "text":
			appendPart(chatText(block.Get("text"), false))
		case "image":
			if err := mediaPropertyError(block.Get("source"), "block.source.type"); err != nil {
				return nil, err
			}
			if requestObject(block.Get("source")).String("type") == "file" {
				appendPart(chatText(requestEncode(chatFileImage), true))
			} else if mode == "text_only" {
				appendPart(chatText(requestEncode(chatOmittedImage), false))
			} else {
				part, err := chatImage(block)
				if err != nil {
					return nil, err
				}
				appendPart(part)
			}
		case "document":
			if err := mediaPropertyError(block.Get("source"), "block.source.type"); err != nil {
				return nil, err
			}
			source := requestObject(block.Get("source"))
			switch source.String("type") {
			case "text":
				prefix, err := mediaDocumentProvenance(block)
				if err != nil {
					return nil, err
				}
				value, err := mediaInterpolation(prefix, source.Get("data"))
				if err != nil {
					return nil, err
				}
				appendPart(chatText(value, true))
			case "content":
				parts, err := chatDocumentParts(block)
				if err != nil {
					return nil, err
				}
				if mode == "text_only" {
					texts := []json.RawMessage{}
					for _, part := range parts {
						if part.String("type") == "image_url" {
							texts = append(texts, requestEncode(chatOmittedImage))
						} else {
							texts = append(texts, part.Get("text"))
						}
					}
					appendPart(chatText(requestJoin(texts, ""), true))
				} else {
					for _, part := range parts {
						appendPart(part)
					}
				}
			default:
				text, err := chatDocumentReference(block)
				if err != nil {
					return nil, err
				}
				appendPart(chatText(text, true))
			}
		case "search_result":
			text := chatSearchText(block)
			if requestTruthy(text) {
				appendPart(chatText(text, true))
			}
		case "tool_result":
			sawTool = true
			text, media, err := chatToolResult(block, mode)
			if err != nil {
				return nil, err
			}
			tools = append(tools, requestEncode(requestRecord(requestField("role", "tool"), protocol.JSONField{Name: "tool_call_id", Value: block.Get("tool_use_id")}, protocol.JSONField{Name: "content", Value: text})))
			if len(media) > 0 && mode != "text_only" {
				groups = append(groups, group{block.Get("tool_use_id"), media})
			}
		}
	}
	output := []json.RawMessage{}
	if len(leading) > 0 {
		message, err := chatCreateUser(leading, mode)
		if err != nil {
			return nil, err
		}
		output = append(output, message)
	}
	output = append(output, tools...)
	if len(groups) > 0 {
		parts := []json.RawMessage{}
		for _, group := range groups {
			marker, err := mediaInterpolation(requestEncode("[Media content for tool call "), group.id, requestEncode("]"))
			if err != nil {
				return nil, err
			}
			parts = append(parts, requestEncode(chatText(marker, false)))
			for _, part := range group.parts {
				parts = append(parts, requestEncode(part))
			}
		}
		output = append(output, requestEncode(requestRecord(requestField("role", "user"), requestField("content", parts))))
	}
	if len(trailing) > 0 {
		message, err := chatCreateUser(trailing, mode)
		if err != nil {
			return nil, err
		}
		output = append(output, message)
	}
	return output, nil
}
