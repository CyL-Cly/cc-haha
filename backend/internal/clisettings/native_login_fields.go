package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativeLoginKeys = []string{"forceLoginMethod", "forceLoginOrgUUID"}

func nativeLoginErrors(input protocol.JSONObject, file string) []json.RawMessage {
	issues := []json.RawMessage{}
	if value := input.Get("forceLoginMethod"); value != nil && string(protocol.StringifyValidatedJavaScriptJSON(value)) != `"claudeai"` && string(protocol.StringifyValidatedJavaScriptJSON(value)) != `"console"` {
		issue := enumError(file, "forceLoginMethod", []string{"claudeai", "console"})
		var record protocol.JSONObject
		_ = record.UnmarshalJSON(issue)
		record.Delete("docLink")
		issue, _ = record.MarshalJSON()
		issues = append(issues, issue)
	}
	if value := input.Get("forceLoginOrgUUID"); value != nil && receivedType(value) != "string" {
		issues = append(issues, validationError(file, "forceLoginOrgUUID", "string", receivedType(value)))
	}
	return issues
}
