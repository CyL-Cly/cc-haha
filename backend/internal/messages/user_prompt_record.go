package messages

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// Only the implemented processUserInput blocking producer is admitted here.
// Original normalization filters this retained warning before API messages.
func IsNativeUserPromptBlockedRecord(record protocol.JSONObject) bool {
	if record.String("type") != "system" || record.String("subtype") != "informational" || record.String("level") != "warning" || string(record.Get("isMeta")) != "false" || record.String("uuid") == "" || record.String("timestamp") == "" || record.Get("isApiErrorMessage") != nil || record.Get("isVirtual") != nil {
		return false
	}
	units, err := protocol.DecodeJavaScriptJSONString(record.Get("content"))
	if err != nil {
		return false
	}
	prefix := "UserPromptSubmit operation blocked by hook:\n"
	if len(units) < len(prefix) {
		return false
	}
	for i := range prefix {
		if units[i] != uint16(prefix[i]) {
			return false
		}
	}
	return true
}
