package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func processedWorkflowKey(raw json.RawMessage) bool {
	return bytes.Equal(raw, quoted("disableWorkflows")) || bytes.Equal(raw, quoted("enableWorkflows")) || bytes.Equal(raw, quoted("workflowKeywordTriggerEnabled"))
}

// These optional fields use the actual release SettingsSchema booleans. A bad
// value rejects the complete settings file through the existing error boundary.
func parseWorkflowFields(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string) {
	for _, key := range []string{"disableWorkflows", "enableWorkflows", "workflowKeywordTriggerEnabled"} {
		raw := input.Get(key)
		if raw == nil {
			continue
		}
		if receivedType(raw) != "boolean" {
			result.Errors = append(result.Errors, validationError(file, key, "boolean", receivedType(raw)))
			continue
		}
		out.Set(key, raw)
	}
}
