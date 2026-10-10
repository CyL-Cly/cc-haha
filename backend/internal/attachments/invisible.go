package attachments

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Invisible follows normalizeAttachmentForAPI's bookkeeping-only cases. Their
// raw records stay in Harness history and contribute no API text or tokens.
func Invisible(payload json.RawMessage) bool {
	var value protocol.JSONObject
	if value.UnmarshalJSON(payload) != nil || !value.IsObject() {
		return false
	}
	switch value.String("type") {
	case "hook_success":
		return value.String("hookEvent") != "SessionStart" && value.String("hookEvent") != "UserPromptSubmit"
	case "already_read_file", "command_permissions", "edited_image_file", "hook_cancelled", "hook_error_during_execution", "hook_non_blocking_error", "hook_system_message", "structured_output", "hook_permission_decision", "dynamic_skill":
		return true
	}
	return false
}
