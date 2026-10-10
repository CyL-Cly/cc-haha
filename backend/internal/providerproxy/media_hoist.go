package providerproxy

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func ShouldHoistNestedToolResultMedia(input protocol.JSONObject) bool {
	return string(input.Get("supportsNestedToolResultMedia")) == "false" && input.String("providerApiFormat") == "anthropic" && input.String("resolvedApiFormat") == "anthropic"
}

func mediaText(text json.RawMessage) json.RawMessage {
	return requestEncode(requestRecord(requestField("type", "text"), protocol.JSONField{Name: "text", Value: text}))
}
func mediaInterpolation(values ...json.RawMessage) (json.RawMessage, error) {
	coerced := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		text, err := protocol.ExactJavaScriptStringJSON(value)
		if err != nil {
			failure := responseError(err.Error())
			failure.Name = "TypeError"
			return nil, failure
		}
		coerced = append(coerced, text)
	}
	return responseConcat(coerced...), nil
}
func mediaDocumentProvenance(document protocol.JSONObject) (json.RawMessage, error) {
	lines := []json.RawMessage{}
	if requestTruthy(document.Get("title")) {
		text, err := mediaInterpolation(requestEncode("[Document: "), document.Get("title"), requestEncode("]"))
		if err != nil {
			return nil, err
		}
		lines = append(lines, text)
	}
	if requestTruthy(document.Get("context")) {
		text, err := mediaInterpolation(requestEncode("[Document context: "), document.Get("context"), requestEncode("]"))
		if err != nil {
			return nil, err
		}
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		return requestEncode(""), nil
	}
	return responseConcat(requestJoin(lines, "\n"), requestEncode("\n")), nil
}
func mediaMarker(toolID json.RawMessage, media []json.RawMessage) (json.RawMessage, error) {
	images := 0
	for _, part := range media {
		if requestObject(part).String("type") == "image" {
			images++
		}
	}
	documents := len(media) - images
	prefix, suffix := "[Image content for tool call ", "]"
	if images > 0 && documents > 0 {
		imageLabel, documentLabel := "image", "document"
		if images != 1 {
			imageLabel = "images"
		}
		if documents != 1 {
			documentLabel = "documents"
		}
		prefix = "[Media content for tool call "
		suffix = ": " + strconv.Itoa(images) + " " + imageLabel + ", " + strconv.Itoa(documents) + " " + documentLabel + "]"
	} else {
		if images == 0 {
			prefix = "[Document content for tool call "
		}
		if len(media) > 1 {
			suffix = " (" + strconv.Itoa(len(media)) + ")]"
		}
	}
	text, err := mediaInterpolation(requestEncode(prefix), toolID, requestEncode(suffix))
	if err != nil {
		return nil, err
	}
	return mediaText(text), nil
}

func mediaPropertyError(raw json.RawMessage, expression string) error {
	if raw != nil && string(raw) != "null" {
		return nil
	}
	kind := "undefined"
	if raw != nil {
		kind = "null"
	}
	failure := responseError(kind + " is not an object (evaluating '" + expression + "')")
	failure.Name = "TypeError"
	return failure
}

func mediaMethodError(raw json.RawMessage, expression string) error {
	if err := mediaPropertyError(raw, expression); err != nil {
		return err
	}
	failure := responseError(mediaMethodErrors[expression])
	failure.Name = "TypeError"
	return failure
}

func unresolvedServerMediaTurn(message protocol.JSONObject) (bool, error) {
	if requestIsString(message.Get("content")) {
		return false, nil
	}
	if !requestIsArray(message.Get("content")) {
		return false, mediaMethodError(message.Get("content"), "msg.content.filter")
	}
	ids, resolved := []json.RawMessage{}, map[string]bool{}
	for _, raw := range requestArray(message.Get("content")) {
		if err := mediaPropertyError(raw, "candidate.id"); err != nil {
			return false, err
		}
		part := requestObject(raw)
		if requestIsString(part.Get("id")) && (part.String("type") == "server_tool_use" || part.String("type") == "mcp_tool_use") {
			ids = append(ids, part.Get("id"))
		}
		if requestIsString(part.Get("tool_use_id")) && strings.HasSuffix(part.String("type"), "_tool_result") {
			resolved[string(protocol.StringifyValidatedJavaScriptJSON(part.Get("tool_use_id")))] = true
		}
	}
	for _, id := range ids {
		if !resolved[string(protocol.StringifyValidatedJavaScriptJSON(id))] {
			return true, nil
		}
	}
	return false, nil
}

func textOnlyDocumentContent(content json.RawMessage) (bool, error) {
	if requestIsString(content) {
		return true, nil
	}
	if !requestIsArray(content) {
		return false, mediaMethodError(content, "source.content.every")
	}
	for _, block := range requestArray(content) {
		if err := mediaPropertyError(block, "block.type"); err != nil {
			return false, err
		}
		if requestObject(block).String("type") != "text" {
			return false, nil
		}
	}
	return true, nil
}

// HoistToolResultMediaForCompatibility keeps tool results contiguous and
// preserves document provenance/cache breakpoints using the original rewrite.
// Native endpoints must call this only when their own declaration opts in.
func HoistToolResultMediaForCompatibility(body protocol.JSONObject) (protocol.JSONObject, error) {
	if !requestIsArray(body.Get("messages")) {
		return protocol.JSONObject{}, mediaMethodError(body.Get("messages"), "body.messages.map")
	}
	original := requestArray(body.Get("messages"))
	output := make([]json.RawMessage, 0, len(original))
	changed := false
	for index, rawMessage := range original {
		if err := mediaPropertyError(rawMessage, "msg.role"); err != nil {
			return protocol.JSONObject{}, err
		}
		message := requestObject(rawMessage)
		if message.String("role") != "user" || requestIsString(message.Get("content")) {
			output = append(output, rawMessage)
			continue
		}
		blocked := false
		for previous := index - 1; previous >= 0; previous-- {
			prior := requestObject(original[previous])
			if prior.String("role") != "assistant" {
				continue
			}
			var err error
			blocked, err = unresolvedServerMediaTurn(prior)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			break
		}
		if blocked {
			output = append(output, rawMessage)
			continue
		}
		if !requestIsArray(message.Get("content")) {
			return protocol.JSONObject{}, mediaMethodError(message.Get("content"), "msg.content.map")
		}
		content, hoisted := []json.RawMessage{}, []json.RawMessage{}
		messageChanged := false
		for _, rawBlock := range requestArray(message.Get("content")) {
			if err := mediaPropertyError(rawBlock, "block.type"); err != nil {
				return protocol.JSONObject{}, err
			}
			block := requestObject(rawBlock)
			if block.String("type") != "tool_result" || !requestIsArray(block.Get("content")) {
				content = append(content, rawBlock)
				continue
			}
			media, retained := []json.RawMessage{}, []json.RawMessage{}
			degraded := false
			for _, rawPart := range requestArray(block.Get("content")) {
				if err := mediaPropertyError(rawPart, "part.type"); err != nil {
					return protocol.JSONObject{}, err
				}
				part := requestObject(rawPart)
				if part.String("type") == "document" {
					if err := mediaPropertyError(part.Get("source"), "part.source.type"); err != nil {
						return protocol.JSONObject{}, err
					}
				}
				source := requestObject(part.Get("source"))
				textOnly := false
				if part.String("type") == "document" && source.String("type") == "content" {
					var err error
					textOnly, err = textOnlyDocumentContent(source.Get("content"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
				}
				switch {
				case part.String("type") == "image":
					media = append(media, rawPart)
				case part.String("type") == "document" && source.String("type") == "text":
					provenance, err := mediaDocumentProvenance(part)
					if err != nil {
						return protocol.JSONObject{}, err
					}
					value, err := mediaInterpolation(provenance, source.Get("data"))
					if err != nil {
						return protocol.JSONObject{}, err
					}
					text := requestRecord(requestField("type", "text"), protocol.JSONField{Name: "text", Value: value})
					text.Set("cache_control", part.Get("cache_control"))
					retained = append(retained, requestEncode(text))
					degraded = true
				case part.String("type") == "document" && source.String("type") == "content" && textOnly:
					blocks := []protocol.JSONObject{}
					for _, field := range []struct{ key, prefix string }{{"title", "[Document: "}, {"context", "[Document context: "}} {
						if requestTruthy(part.Get(field.key)) {
							text, err := mediaInterpolation(requestEncode(field.prefix), part.Get(field.key), requestEncode("]"))
							if err != nil {
								return protocol.JSONObject{}, err
							}
							blocks = append(blocks, requestObject(mediaText(text)))
						}
					}
					if requestIsString(source.Get("content")) {
						blocks = append(blocks, requestObject(mediaText(source.Get("content"))))
					} else {
						for _, raw := range requestArray(source.Get("content")) {
							inner := requestObject(raw)
							text := requestObject(mediaText(inner.Get("text")))
							text.Set("cache_control", inner.Get("cache_control"))
							text.Set("citations", inner.Get("citations"))
							blocks = append(blocks, text)
						}
					}
					if len(blocks) > 0 && part.Get("cache_control") != nil && blocks[len(blocks)-1].Get("cache_control") == nil {
						blocks[len(blocks)-1].Set("cache_control", part.Get("cache_control"))
					}
					for _, block := range blocks {
						retained = append(retained, requestEncode(block))
					}
					degraded = true
				case part.String("type") == "document":
					media = append(media, rawPart)
				default:
					retained = append(retained, rawPart)
				}
			}
			if len(media) == 0 && !degraded {
				content = append(content, rawBlock)
				continue
			}
			messageChanged = true
			if len(media) > 0 {
				marker, err := mediaMarker(block.Get("tool_use_id"), media)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				hoisted = append(hoisted, marker)
				hoisted = append(hoisted, media...)
				if len(retained) == 0 {
					retained = append(retained, mediaText(requestEncode("Media result attached after this tool result.")))
				}
			}
			copy := block.Clone()
			copy.Set("content", requestEncode(retained))
			content = append(content, requestEncode(copy))
		}
		if !messageChanged {
			output = append(output, rawMessage)
			continue
		}
		changed = true
		insertAt := len(content)
		for at := len(content) - 1; at >= 0; at-- {
			if requestObject(content[at]).String("type") == "tool_result" {
				insertAt = at + 1
				break
			}
		}
		merged := make([]json.RawMessage, 0, len(content)+len(hoisted))
		merged = append(merged, content[:insertAt]...)
		merged = append(merged, hoisted...)
		merged = append(merged, content[insertAt:]...)
		copy := message.Clone()
		copy.Set("content", requestEncode(merged))
		output = append(output, requestEncode(copy))
	}
	if !changed {
		return body, nil
	}
	copy := body.Clone()
	copy.Set("messages", requestEncode(output))
	return copy, nil
}
