package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// QueryEngine collects the actual canUseTool result for this ASK, including
// denials which become tool feedback instead of stopping the query. The input
// is the input passed to permission resolution, not a later updatedInput.
func (h *Host) recordPermissionDenial(tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext, decision queryloop.ToolPermission) {
	if decision.Behavior == "allow" {
		return
	}
	name := tool.Name
	if name == "Agent" {
		name = "Task"
	}
	h.permissionDenials = append(h.permissionDenials, protocol.NewJSONObject(
		protocol.JSONField{Name: "tool_name", Value: quote(name)},
		protocol.JSONField{Name: "tool_use_id", Value: append(json.RawMessage(nil), call.ToolUseID...)},
		protocol.JSONField{Name: "tool_input", Value: raw(input)},
	))
}

func (h *Host) permissionDenialsJSON() json.RawMessage {
	if len(h.permissionDenials) == 0 {
		return json.RawMessage("[]")
	}
	return raw(h.permissionDenials)
}
