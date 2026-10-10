package queryloop

import (
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Permission content is already processed by its UI/SDK caller. Source appends
// these blocks at user-message level, after tool_result and acceptFeedback.
// In particular, rejected images must not enter an is_error tool_result.
func permissionUserContent(mapped protocol.JSONObject, permission ToolPermission, history []Message) (storage.Object, error) {
	first, err := mapped.MarshalJSON()
	if err != nil {
		return nil, err
	}
	content := []json.RawMessage{first}
	if permission.Behavior == "allow" && permissionFeedbackTruthy(permission.AcceptFeedback) {
		feedback := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("text")}, protocol.JSONField{Name: "text", Value: permission.AcceptFeedback})
		raw, err := feedback.MarshalJSON()
		if err != nil {
			return nil, err
		}
		content = append(content, raw)
	}
	blocks := permission.ContentBlocks
	if permission.Behavior != "allow" && permission.Behavior != "ask" {
		blocks = nil
	}
	images := 0
	for _, raw := range blocks {
		var block protocol.JSONObject
		if err := block.UnmarshalJSON(raw); err != nil || !block.IsObject() {
			return nil, unsupported("permission content block object", err)
		}
		if block.String("type") == "image" {
			images++
		}
		content = append(content, raw)
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	result := storage.Object{"content": encoded}
	if images > 0 {
		maxID := 0.0
		for _, message := range history {
			if message.Record.String("type") != "user" {
				continue
			}
			var ids []float64
			raw := message.Record.Get("imagePasteIds")
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			if err := json.Unmarshal(raw, &ids); err != nil {
				return nil, unsupported("historical image paste ID coercion", err)
			}
			for _, id := range ids {
				if !math.IsNaN(id) && id > maxID {
					maxID = id
				}
			}
		}
		ids := make([]float64, images)
		for i := range ids {
			ids[i] = maxID + 1 + float64(i)
		}
		result["imagePasteIds"], err = json.Marshal(ids)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func permissionFeedbackTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0
	}
	return true
}
