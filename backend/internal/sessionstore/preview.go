package sessionstore

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type previewState struct {
	truncated        bool
	remaining, nodes int
}

var previewIdentities = map[string]bool{"id": true, "type": true, "role": true, "name": true, "tool_use_id": true, "agentId": true, "backgroundTaskId": true, "background_task_id": true}

type pair struct{ key, value json.RawMessage }

func objectPairs(raw []byte) ([]pair, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	pairs := []pair{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		key := raw[start:decoder.InputOffset()]
		key = key[bytes.IndexByte(key, '"'):]
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{key, value})
	}
	return pairs, nil
}
func (state *previewState) preview(raw json.RawMessage, depth int) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		limit := state.remaining
		if limit > 16384 {
			limit = 16384
		}
		if limit < 0 {
			limit = 0
		}
		used := len(units)
		if used > limit {
			used = limit
		}
		state.remaining -= used
		if len(units) > limit {
			state.truncated = true
			units = append(append([]uint16(nil), units[:limit]...), utf16.Encode([]rune("\n… [truncated preview]"))...)
		}
		return protocol.QuoteJavaScriptUTF16(units)
	}
	if raw[0] != '{' && raw[0] != '[' {
		return raw
	}
	state.nodes--
	if state.nodes < 0 || depth > 12 {
		state.truncated = true
		return []byte(`"[truncated preview]"`)
	}
	if raw[0] == '[' {
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		if len(items) > 256 {
			state.truncated = true
			items = items[:256]
		}
		output := []byte{'['}
		for index, item := range items {
			if index > 0 {
				output = append(output, ',')
			}
			output = append(output, state.preview(item, depth+1)...)
		}
		return append(output, ']')
	}
	pairs, _ := objectPairs(raw)
	if len(pairs) > 256 {
		state.truncated = true
		pairs = pairs[:256]
	}
	output := []byte{'{'}
	for index, pair := range pairs {
		if index > 0 {
			output = append(output, ',')
		}
		output = append(output, pair.key...)
		output = append(output, ':')
		var key string
		_ = json.Unmarshal(pair.key, &key)
		child := pair.value
		keep := false
		if previewIdentities[key] && len(child) > 0 && child[0] == '"' {
			units, _ := protocol.DecodeJavaScriptJSONString(child)
			keep = len(units) <= 4096
		}
		if !keep {
			child = state.preview(child, depth+1)
		}
		output = append(output, child...)
	}
	return append(output, '}')
}
func spreadObject(raw json.RawMessage) protocol.JSONObject {
	object := protocol.NewJSONObject()
	if len(raw) == 0 {
		return object
	}
	if raw[0] == '{' {
		_ = object.UnmarshalJSON(raw)
		return object
	}
	if raw[0] == '[' {
		var entries []json.RawMessage
		_ = json.Unmarshal(raw, &entries)
		for index, value := range entries {
			object.Set(strconv.Itoa(index), value)
		}
	} else if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		for index, unit := range units {
			object.Set(strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return object
}

// DisplayPreview follows the source's display-only UTF-16/node budget. Durable
// replay and state reducers must continue using the original record instead.
func DisplayPreview(raw json.RawMessage) (json.RawMessage, error) {
	var original protocol.JSONObject
	if err := original.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return nil, err
	}
	var entry protocol.JSONObject
	if err = entry.UnmarshalJSON(normalized); err != nil || !entry.IsObject() {
		return nil, &APIError{400, "BAD_REQUEST", "Display preview requires an object"}
	}
	state := previewState{remaining: 48 * 1024, nodes: 2048}
	result := entry.Clone()
	if content := entry.Get("content"); len(content) > 0 {
		result.Set("content", state.preview(content, 0))
	}
	if rawMessage := entry.Get("message"); truthy(original.Get("message")) {
		message := spreadObject(rawMessage)
		message.Set("content", state.preview(message.Get("content"), 0))
		value, _ := message.MarshalJSON()
		result.Set("message", value)
	}
	if toolResult := entry.Get("toolUseResult"); len(toolResult) > 0 {
		result.Set("toolUseResult", state.preview(toolResult, 0))
	}
	if state.truncated {
		result.Set("bodyTruncated", []byte("true"))
	}
	output, err := result.MarshalJSON()
	return output, err
}
