package messages

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// OrderedUser projects the actual factory's user result in Source envelope
// order. It does not add fields, decode UTF-16 strings or accept a prepared API
// message as an original factory input.
func OrderedUser(value storage.Object) protocol.JSONObject {
	var api protocol.JSONObject
	_ = api.UnmarshalJSON(value["message"])
	orderedAPI := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: api.Get("role")}, protocol.JSONField{Name: "content", Value: api.Get("content")})
	encoded, _ := json.Marshal(orderedAPI)
	result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: value["type"]}, protocol.JSONField{Name: "message", Value: encoded})
	for _, name := range []string{"isMeta", "isVisibleInTranscriptOnly", "isVirtual", "isCompactSummary", "summarizeMetadata", "uuid", "timestamp", "toolUseResult", "mcpMeta", "imagePasteIds", "sourceToolAssistantUUID", "permissionMode", "origin"} {
		result.Set(name, value[name])
	}
	return result
}
