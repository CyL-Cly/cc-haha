package contextusage

import (
	"encoding/json"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var apiContextAnnotation = regexp.MustCompile(`(?i)\[(1|2)m\]`)

func WireModel(model string) string { return apiContextAnnotation.ReplaceAllString(model, "") }

func thinkingBlocks(messages []protocol.JSONObject) bool {
	for _, message := range messages {
		if message.String("role") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "thinking" || block.String("type") == "redacted_thinking" {
				return true
			}
		}
	}
	return false
}

func CountRequest(model string, messages, tools []protocol.JSONObject, betas []string) protocol.JSONObject {
	if len(messages) == 0 {
		messages = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: quote("foo")})}
	}
	rows, _ := json.Marshal(messages)
	schemas, _ := json.Marshal(tools)
	if tools == nil {
		schemas = []byte("[]")
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quote(WireModel(model))}, protocol.JSONField{Name: "messages", Value: rows}, protocol.JSONField{Name: "tools", Value: schemas})
	if len(betas) > 0 {
		rows, _ := json.Marshal(betas)
		body.Set("betas", rows)
	}
	if thinkingBlocks(messages) {
		body.Set("thinking", json.RawMessage(`{"type":"enabled","budget_tokens":1024}`))
	}
	return body
}

// StripCountingSearchFields matches the Source fallback's own copy projection.
// Plain messages retain unknown properties; tool_use deliberately retains only
// type/id/name/input and tool references are removed from tool-result contents.
func StripCountingSearchFields(messages []protocol.JSONObject) []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, message := range messages {
		copy := message.Clone()
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil {
			result = append(result, copy)
			continue
		}
		for i, block := range blocks {
			if block.String("type") == "tool_use" {
				next := protocol.NewJSONObject()
				for _, key := range []string{"type", "id", "name", "input"} {
					if block.Get(key) != nil {
						next.Set(key, block.Get(key))
					}
				}
				blocks[i] = next
			} else if block.String("type") == "tool_result" {
				var content []protocol.JSONObject
				if json.Unmarshal(block.Get("content"), &content) != nil {
					continue
				}
				filtered := []protocol.JSONObject{}
				for _, part := range content {
					if part.String("type") != "tool_reference" {
						filtered = append(filtered, part)
					}
				}
				changed := len(filtered) != len(content) || len(filtered) == 0
				if len(filtered) == 0 {
					filtered = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: quote("[tool references]")})}
				}
				if changed {
					next := block.Clone()
					rows, _ := json.Marshal(filtered)
					next.Set("content", rows)
					blocks[i] = next
				}
			}
		}
		rows, _ := json.Marshal(blocks)
		copy.Set("content", rows)
		result = append(result, copy)
	}
	return result
}

func HaikuCountRequest(model string, messages, tools []protocol.JSONObject, betas []string, metadata protocol.JSONObject, extra protocol.JSONObject) protocol.JSONObject {
	thinking := thinkingBlocks(messages)
	messages = StripCountingSearchFields(messages)
	if len(messages) == 0 {
		messages = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: quote("count")})}
	}
	rows, _ := json.Marshal(messages)
	max := json.RawMessage("1")
	if thinking {
		max = json.RawMessage("2048")
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quote(WireModel(model))}, protocol.JSONField{Name: "max_tokens", Value: max}, protocol.JSONField{Name: "messages", Value: rows})
	if len(tools) > 0 {
		rows, _ := json.Marshal(tools)
		body.Set("tools", rows)
	}
	if len(betas) > 0 {
		rows, _ := json.Marshal(betas)
		body.Set("betas", rows)
	}
	encoded, _ := metadata.MarshalJSON()
	body.Set("metadata", encoded)
	for _, property := range extra.OwnProperties() {
		_ = body.SetPropertyJSON(property.NameJSON, property.Value)
	}
	if thinking {
		body.Set("thinking", json.RawMessage(`{"type":"enabled","budget_tokens":1024}`))
	}
	return body
}
