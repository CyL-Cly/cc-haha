package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Shared CLI startup retains these fields for the native updater/installer.
// Permission/query admission uses the same schema as the physical settings.
func IsNativeInstallationSetting(key string, value json.RawMessage) bool {
	switch key {
	case "autoUpdatesChannel":
		return len(nativeInstallationChannelErrors(value, "")) == 0
	case "minimumVersion":
		return len(nativeInstallationMinimumErrors(value, "")) == 0
	}
	return false
}

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
