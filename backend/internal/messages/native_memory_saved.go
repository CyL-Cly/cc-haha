package messages

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original REPL memory notification stays in host/transcript history.
// API preparation filters this system class; it is never model-visible input.
func IsNativeMemorySavedRecord(record protocol.JSONObject) bool {
	if record.String("type") != "system" || record.String("subtype") != "memory_saved" || record.String("uuid") == "" || record.String("timestamp") == "" || string(record.Get("isMeta")) != "false" || record.Get("isVirtual") != nil || record.Get("isApiErrorMessage") != nil {
		return false
	}
	var paths []json.RawMessage
	raw := record.Get("writtenPaths")
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &paths) != nil {
		return false
	}
	for _, path := range paths {
		if _, err := protocol.DecodeJavaScriptJSONString(path); err != nil {
			return false
		}
	}
	return true
}
