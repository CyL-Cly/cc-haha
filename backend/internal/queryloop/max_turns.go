package queryloop

import (
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source query.ts emits this only after tools have completed and another model
// turn would be needed. Allocation belongs to the same query UUID/clock sources.
func maxTurnsMessage(options Options, nextTurn int) Message {
	attachment := protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quoted("max_turns_reached")},
		protocol.JSONField{Name: "maxTurns", Value: json.RawMessage(intJSON(options.MaxTurns))},
		protocol.JSONField{Name: "turnCount", Value: json.RawMessage(intJSON(nextTurn))},
	)
	return Message{Record: protocol.NewJSONObject(
		protocol.JSONField{Name: "attachment", Value: mustObjectJSON(attachment)},
		protocol.JSONField{Name: "type", Value: quoted("attachment")},
		protocol.JSONField{Name: "uuid", Value: quoted(options.UUID())},
		protocol.JSONField{Name: "timestamp", Value: quoted(time.UnixMilli(options.Now().UnixMilli()).UTC().Format("2006-01-02T15:04:05.000Z"))},
	)}
}
func intJSON(value int) string { raw, _ := json.Marshal(value); return string(raw) }
func mustObjectJSON(value protocol.JSONObject) json.RawMessage {
	raw, _ := value.MarshalJSON()
	return raw
}
