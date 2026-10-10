package attachments

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These raw state reminders remain in the same original Harness history.
func Ultracode(payload json.RawMessage) bool {
	var value protocol.JSONObject
	if value.UnmarshalJSON(payload) != nil {
		return false
	}
	switch value.String("type") {
	case "workflow_keyword_request", "ultra_effort_enter", "ultra_effort_exit":
		return true
	}
	return false
}
