package attachments

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ReorderForAPI runs before virtual filtering. Attachments remain in their
// original relative order and stop only at assistants or first-block results.
func ReorderForAPI(input []protocol.JSONObject) []protocol.JSONObject {
	result := make([]protocol.JSONObject, 0, len(input))
	pending := []protocol.JSONObject{}
	for index := len(input) - 1; index >= 0; index-- {
		message := input[index]
		kind := message.String("type")
		if kind == "attachment" {
			pending = append(pending, message)
			continue
		}
		stop := kind == "assistant"
		if kind == "user" {
			var api protocol.JSONObject
			var blocks []protocol.JSONObject
			if json.Unmarshal(message.Get("message"), &api) == nil && json.Unmarshal(api.Get("content"), &blocks) == nil && len(blocks) > 0 {
				stop = blocks[0].String("type") == "tool_result"
			}
		}
		if stop && len(pending) > 0 {
			result = append(result, pending...)
			pending = nil
		}
		result = append(result, message)
	}
	result = append(result, pending...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}
