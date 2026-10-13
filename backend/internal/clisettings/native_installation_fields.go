package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func nativeInstallationChannelErrors(value json.RawMessage, file string) []json.RawMessage {
	if value == nil {
		return nil
	}
	encoded := protocol.StringifyValidatedJavaScriptJSON(value)
	if string(encoded) == `"latest"` || string(encoded) == `"stable"` {
		return nil
	}
	var record protocol.JSONObject
	_ = record.UnmarshalJSON(enumError(file, "autoUpdatesChannel", []string{"latest", "stable"}))
	record.Delete("docLink")
	issue, _ := record.MarshalJSON()
	return []json.RawMessage{issue}
}

func nativeInstallationMinimumErrors(value json.RawMessage, file string) []json.RawMessage {
	if value == nil || receivedType(value) == "string" {
		return nil
	}
	return []json.RawMessage{validationError(file, "minimumVersion", "string", receivedType(value))}
}
