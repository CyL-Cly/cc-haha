package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func (h *Host) emitNativeGoalOutput(scope *cooperative.Scope, record protocol.JSONObject, goalCommand ...bool) error {
	initial := len(goalCommand) == 1 && goalCommand[0] && (strings.Contains(record.String("content"), "<local-command-stdout>") || strings.Contains(record.String("content"), "<local-command-stderr>"))
	if record.String("type") != "system" || record.String("subtype") != "local_command" || !initial && !goalstate.IsLocalOutput(record.String("content")) {
		return nil
	}
	output := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("local_command_output")}, protocol.JSONField{Name: "content", Value: record.Get("content")}, protocol.JSONField{Name: "uuid", Value: record.Get("uuid")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())})
	_, err := h.enqueue(scope, output)
	return err
}
