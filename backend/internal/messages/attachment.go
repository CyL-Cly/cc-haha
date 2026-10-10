package messages

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (factory *Factory) CreateAttachment(payload json.RawMessage) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "attachment", Value: payload}, protocol.JSONField{Name: "type", Value: stringJSON("attachment")}, protocol.JSONField{Name: "uuid", Value: stringJSON(factory.uuid())}, protocol.JSONField{Name: "timestamp", Value: factory.timestamp()})
}
