package application

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Mirrors conversationService.normalizeSessionPermissionUpdates, including
// malformed suggestions which the SDK schema subsequently rejects as a batch.
func normalizeSessionPermissionUpdates(suggestions json.RawMessage, toolName string) json.RawMessage {
	var rows []json.RawMessage
	if json.Unmarshal(suggestions, &rows) == nil && len(rows) > 0 {
		for i, row := range rows {
			var spread protocol.JSONObject
			if spread.UnmarshalJSON(row) != nil || !spread.IsObject() {
				var entries []json.RawMessage
				if json.Unmarshal(row, &entries) != nil || entries == nil {
					continue
				}
				spread = protocol.NewJSONObject()
				for index, value := range entries {
					spread.Set(strconv.Itoa(index), value)
				}
			}
			spread.Set("destination", raw("session"))
			rows[i], _ = spread.MarshalJSON()
		}
		return raw(rows)
	}
	return raw([]protocol.JSONObject{protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: raw("addRules")},
		protocol.JSONField{Name: "rules", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: raw(toolName)})})},
		protocol.JSONField{Name: "behavior", Value: raw("allow")},
		protocol.JSONField{Name: "destination", Value: raw("session")},
	)})
}
