package messages

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (f *Factory) CreateSystemInformation(content json.RawMessage, level string) protocol.JSONObject {
	// createSystemMessage evaluates timestamp before UUID and keeps the optional
	// toolUseID undefined, between UUID and level, at the JSON boundary.
	timestamp := f.timestamp()
	id := stringJSON(f.uuid())
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: stringJSON("system")}, protocol.JSONField{Name: "subtype", Value: stringJSON("informational")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "isMeta", Value: []byte("false")}, protocol.JSONField{Name: "timestamp", Value: timestamp}, protocol.JSONField{Name: "uuid", Value: id}, protocol.JSONField{Name: "toolUseID", Value: nil}, protocol.JSONField{Name: "level", Value: stringJSON(level)})
}
