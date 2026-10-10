package messages

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (f *Factory) CreateProgress(toolUseID, parentToolUseID json.RawMessage, data protocol.JSONObject) protocol.JSONObject {
	bytes, _ := data.MarshalJSON()
	quote := func(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("progress")}, protocol.JSONField{Name: "data", Value: bytes}, protocol.JSONField{Name: "toolUseID", Value: toolUseID}, protocol.JSONField{Name: "parentToolUseID", Value: parentToolUseID}, protocol.JSONField{Name: "uuid", Value: quote(f.uuid())}, protocol.JSONField{Name: "timestamp", Value: quote(f.now().UTC().Format("2006-01-02T15:04:05.000Z"))})
}
