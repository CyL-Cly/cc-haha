package preflightcore

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RoughAPIRequest is the original token estimator over API message objects.
// Callers supply actual native normalized messages and rendered tool schemas.
func RoughAPIRequest(messages, schemas []protocol.JSONObject) (float64, error) {
	total := float64(0)
	for _, message := range messages {
		role := message.Get("role")
		if len(role) == 0 || string(role) == "null" {
			role = json.RawMessage(`""`)
		}
		count, err := RoughContent(role)
		if err != nil {
			return 0, err
		}
		total += count
		count, err = RoughContent(message.Get("content"))
		if err != nil {
			return 0, err
		}
		total += count
	}
	if len(schemas) > 0 {
		rows, err := json.Marshal(schemas)
		if err != nil {
			return 0, err
		}
		tools := protocol.NewJSONObject(protocol.JSONField{Name: "tools", Value: rows})
		serialized, err := tools.MarshalJSON()
		if err != nil {
			return 0, err
		}
		serialized = protocol.StringifyValidatedJavaScriptJSON(serialized)
		text, err := json.Marshal(string(serialized))
		if err != nil {
			return 0, err
		}
		count, err := RoughContent(text)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}
