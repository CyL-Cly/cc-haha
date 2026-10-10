package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

var emptyHookEvents = strings.Fields(`PreToolUse PostToolUse PostToolUseFailure PermissionRequest Notification UserPromptSubmit SessionStart SessionEnd Stop StopFailure SubagentStart SubagentStop PreCompact PostCompact Setup TeammateIdle TaskCompleted Elicitation ElicitationResult ConfigChange WorktreeCreate WorktreeRemove InstructionsLoaded PermissionDenied TaskCreated CwdChanged FileChanged`)

func checkEmptyHooks(raw json.RawMessage) error {
	if receivedType(raw) != "object" {
		return nil
	}
	var hooks protocol.JSONObject
	_ = hooks.UnmarshalJSON(raw)
	for _, property := range hooks.OwnProperties() {
		valid := false
		for _, name := range emptyHookEvents {
			if bytes.Equal(property.NameJSON, quoted(name)) {
				valid = true
				break
			}
		}
		if !valid {
			return &CapabilityError{"unknown hook event schema"}
		}
		var entries []json.RawMessage
		if json.Unmarshal(property.Value, &entries) != nil || receivedType(property.Value) != "array" || len(entries) > 0 {
			return &CapabilityError{"active hook matcher/definition schema"}
		}
	}
	return nil
}
func parseEmptyHooks(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string) {
	if value := input.Get("hooks"); value != nil {
		if receivedType(value) != "object" {
			result.Errors = append(result.Errors, validationError(file, "hooks", "record", receivedType(value)))
		} else {
			out.Set("hooks", value)
		}
	}
	for _, name := range []string{"disableAllHooks", "allowManagedHooksOnly"} {
		value := input.Get(name)
		if value == nil {
			continue
		}
		if receivedType(value) != "boolean" {
			result.Errors = append(result.Errors, validationError(file, name, "boolean", receivedType(value)))
		} else {
			out.Set(name, value)
		}
	}
}
func processedEmptyHooksKey(key json.RawMessage) bool {
	return bytes.Equal(key, quoted("hooks")) || bytes.Equal(key, quoted("disableAllHooks")) || bytes.Equal(key, quoted("allowManagedHooksOnly"))
}
