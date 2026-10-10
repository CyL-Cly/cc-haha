package messages

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// Original hook progress/summary envelopes retained by QueryEngine. They are
// bookkeeping in API normalization, while SDK hook events use a separate bus.
func IsNativeHookRecord(record protocol.JSONObject) bool {
	if record.String("uuid") == "" || record.String("timestamp") == "" {
		return false
	}
	if record.String("type") == "system" && record.String("subtype") == "stop_hook_summary" {
		return record.Get("hookCount") != nil && record.Get("hookInfos") != nil && record.Get("hookErrors") != nil && record.Get("preventedContinuation") != nil && record.String("level") == "suggestion"
	}
	if record.String("type") != "progress" {
		return false
	}
	data, ok := record.Object("data")
	if !ok || data.String("type") != "hook_progress" || record.String("toolUseID") == "" || record.String("parentToolUseID") == "" {
		return false
	}
	event, name := data.String("hookEvent"), data.String("hookName")
	if data.Get("command") == nil {
		return false
	}
	if event == "Stop" || event == "SubagentStop" || event == "TaskCreated" || event == "TaskCompleted" || event == "TeammateIdle" {
		return name == event
	}
	if event == "PreToolUse" || event == "PostToolUse" || event == "PostToolUseFailure" || event == "PermissionRequest" {
		return strings.HasPrefix(name, event+":") && len(name) > len(event)+1
	}
	if event == "SessionStart" {
		return name == "SessionStart:startup" || name == "SessionStart:resume" || name == "SessionStart:clear" || name == "SessionStart:compact"
	}
	return false
}
