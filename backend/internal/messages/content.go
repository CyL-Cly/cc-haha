package messages

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Content blocks must retain property insertion order through every history
// merge and repair, because the next request signs its complete JSON bytes.
type ContentBlock = protocol.JSONObject

func textBlock(text json.RawMessage) ContentBlock {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: stringJSON("text")}, protocol.JSONField{Name: "text", Value: text})
}

func cloneObject(source storage.Object) storage.Object {
	result := make(storage.Object, len(source))
	for field, value := range source {
		result[field] = append(json.RawMessage(nil), value...)
	}
	return result
}

func blockType(block ContentBlock) string { return rawStringField(block.Get("type")) }
func rawStringField(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func whitespace(unit uint16) bool {
	return unit == 0x9 || unit == 0xa || unit == 0xb || unit == 0xc || unit == 0xd || unit == 0x20 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}

func trimText(raw json.RawMessage) (json.RawMessage, bool, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, false, err
	}
	start, end := 0, len(units)
	for start < end && whitespace(units[start]) {
		start++
	}
	for start < end && whitespace(units[end-1]) {
		end--
	}
	return protocol.QuoteJavaScriptUTF16(units[start:end]), start != end, nil
}

func isString(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && value[0] == '"'
}

func contentBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if isString(raw) {
		return []ContentBlock{textBlock(raw)}, nil
	}
	var content []ContentBlock
	if err := json.Unmarshal(raw, &content); err != nil || content == nil {
		return nil, errors.New("message content must be a string or block array")
	}
	return content, nil
}

func smooshIntoToolResult(result ContentBlock, incoming []ContentBlock) (ContentBlock, bool, error) {
	if len(incoming) == 0 {
		return result, true, nil
	}
	existing := result.Get("content")
	var base []ContentBlock
	if len(existing) > 0 && !isString(existing) {
		var err error
		base, err = contentBlocks(existing)
		if err != nil {
			return ContentBlock{}, false, err
		}
		for _, block := range base {
			if blockType(block) == "tool_reference" {
				return ContentBlock{}, false, nil
			}
		}
	}
	if truthy(result.Get("is_error")) {
		filtered := make([]ContentBlock, 0, len(incoming))
		for _, block := range incoming {
			if blockType(block) == "text" {
				filtered = append(filtered, block)
			}
		}
		incoming = filtered
		if len(incoming) == 0 {
			return result, true, nil
		}
	}
	allText := true
	for _, block := range incoming {
		if blockType(block) != "text" {
			allText = false
		}
	}
	if allText && (existing == nil || isString(existing)) {
		parts := make([]json.RawMessage, 0, len(incoming)+1)
		if existing != nil {
			trimmed, nonempty, err := trimText(existing)
			if err != nil {
				return ContentBlock{}, false, err
			}
			if nonempty {
				parts = append(parts, trimmed)
			}
		}
		for _, block := range incoming {
			trimmed, nonempty, err := trimText(block.Get("text"))
			if err != nil {
				return ContentBlock{}, false, err
			}
			if nonempty {
				parts = append(parts, trimmed)
			}
		}
		joined, err := protocol.JoinJavaScriptValuesJSON(parts, "\n\n")
		if err != nil {
			return ContentBlock{}, false, err
		}
		copy := result.Clone()
		copy.Set("content", joined)
		return copy, true, nil
	}
	if isString(existing) {
		trimmed, nonempty, err := trimText(existing)
		if err != nil {
			return ContentBlock{}, false, err
		}
		if nonempty {
			base = []ContentBlock{textBlock(trimmed)}
		}
	}
	merged := make([]ContentBlock, 0, len(base)+len(incoming))
	for _, block := range append(append([]ContentBlock{}, base...), incoming...) {
		if blockType(block) != "text" {
			merged = append(merged, block)
			continue
		}
		trimmed, nonempty, err := trimText(block.Get("text"))
		if err != nil {
			return ContentBlock{}, false, err
		}
		if !nonempty {
			continue
		}
		if len(merged) > 0 && blockType(merged[len(merged)-1]) == "text" {
			previous := merged[len(merged)-1].Clone()
			joined, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{previous.Get("text"), trimmed}, "\n\n")
			if err != nil {
				return ContentBlock{}, false, err
			}
			previous.Set("text", joined)
			merged[len(merged)-1] = previous
		} else {
			merged = append(merged, textBlock(trimmed))
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return ContentBlock{}, false, err
	}
	copy := result.Clone()
	copy.Set("content", raw)
	return copy, true, nil
}

// MergeUserContentBlocks preserves both cached gate profiles. The caller
// supplies its already-frozen tengu_chair_sermon value; this function never
// reads configuration or invents a new default.
func MergeUserContentBlocks(a, b []ContentBlock, universalSmoosh bool) ([]ContentBlock, error) {
	combined := append(append([]ContentBlock{}, a...), b...)
	if len(a) == 0 || blockType(a[len(a)-1]) != "tool_result" {
		return combined, nil
	}
	last := a[len(a)-1]
	if !universalSmoosh {
		if !isString(last.Get("content")) {
			return combined, nil
		}
		for _, block := range b {
			if blockType(block) != "text" {
				return combined, nil
			}
		}
		smooshed, _, err := smooshIntoToolResult(last, b)
		if err != nil {
			return nil, err
		}
		return append(append([]ContentBlock{}, a[:len(a)-1]...), smooshed), nil
	}
	incoming, toolResults := []ContentBlock{}, []ContentBlock{}
	for _, block := range b {
		if blockType(block) == "tool_result" {
			toolResults = append(toolResults, block)
		} else {
			incoming = append(incoming, block)
		}
	}
	if len(incoming) == 0 {
		return combined, nil
	}
	smooshed, possible, err := smooshIntoToolResult(last, incoming)
	if err != nil {
		return nil, err
	}
	if !possible {
		return combined, nil
	}
	return append(append(append([]ContentBlock{}, a[:len(a)-1]...), smooshed), toolResults...), nil
}

func hoistToolResults(content []ContentBlock) []ContentBlock {
	results, other := []ContentBlock{}, []ContentBlock{}
	for _, block := range content {
		if blockType(block) == "tool_result" {
			results = append(results, block)
		} else {
			other = append(other, block)
		}
	}
	return append(results, other...)
}

func mergeMessages(a, b storage.Object, toolResults, universalSmoosh, assistant bool) (storage.Object, error) {
	apiA, validA, errA := storage.NormalizeObject(a["message"])
	apiB, validB, errB := storage.NormalizeObject(b["message"])
	if errA != nil || errB != nil || !validA || !validB {
		return nil, errors.New("merging requires message envelopes")
	}
	contentA, err := contentBlocks(apiA["content"])
	if err != nil {
		return nil, err
	}
	contentB, err := contentBlocks(apiB["content"])
	if err != nil {
		return nil, err
	}
	var content []ContentBlock
	if assistant {
		content = ReorderAssistantToolUseBlocks(append(append([]ContentBlock{}, contentA...), contentB...))
	} else if toolResults {
		content, err = MergeUserContentBlocks(contentA, contentB, universalSmoosh)
		if err != nil {
			return nil, err
		}
	} else {
		content = append(append([]ContentBlock{}, contentA...), contentB...)
		if len(contentA) > 0 && len(contentB) > 0 && blockType(contentA[len(contentA)-1]) == "text" && blockType(contentB[0]) == "text" {
			seam := contentA[len(contentA)-1].Clone()
			joined, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{seam.Get("text"), stringJSON("\n")}, "")
			if err != nil {
				return nil, err
			}
			seam.Set("text", joined)
			content[len(contentA)-1] = seam
		}
	}
	if !assistant {
		content = hoistToolResults(content)
	}
	api := cloneObject(apiA)
	api["content"], err = json.Marshal(content)
	if err != nil {
		return nil, err
	}
	message := cloneObject(a)
	message["message"] = objectJSON(api)
	if !assistant && !toolResults && truthy(a["isMeta"]) {
		if uuid, exists := b["uuid"]; exists {
			message["uuid"] = append(json.RawMessage(nil), uuid...)
		} else {
			delete(message, "uuid")
		}
	}
	return message, nil
}

// MergeUserMessages follows the current distribution's HISTORY_SNIP-off
// profile. The gated snip runtime is not enabled in that frozen build.
func MergeUserMessages(a, b storage.Object) (storage.Object, error) {
	return mergeMessages(a, b, false, false, false)
}
func MergeUserMessagesAndToolResults(a, b storage.Object, universalSmoosh bool) (storage.Object, error) {
	return mergeMessages(a, b, true, universalSmoosh, false)
}
func MergeAssistantMessages(a, b storage.Object) (storage.Object, error) {
	return mergeMessages(a, b, false, false, true)
}

func ReorderAssistantToolUseBlocks(content []ContentBlock) []ContentBlock {
	first, last, count := -1, -1, 0
	for index, block := range content {
		if blockType(block) == "tool_use" {
			if first < 0 {
				first = index
			}
			last, count = index, count+1
		}
	}
	if count < 2 {
		return content
	}
	tools, displaced := []ContentBlock{}, []ContentBlock{}
	for _, block := range content[first : last+1] {
		if blockType(block) == "tool_use" {
			tools = append(tools, block)
		} else {
			displaced = append(displaced, block)
		}
	}
	if len(displaced) == 0 {
		return content
	}
	return append(append(append(append([]ContentBlock{}, content[:first]...), tools...), displaced...), content[last+1:]...)
}
