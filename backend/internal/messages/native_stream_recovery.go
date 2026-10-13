package messages

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func IsNativeStreamRecoveryRecord(record protocol.JSONObject) bool {
	if record.String("type") != "system" || record.String("subtype") != "streaming_fallback" || record.String("level") != "info" || record.String("uuid") == "" || record.String("timestamp") == "" || record.Get("isVirtual") != nil || record.Get("isApiErrorMessage") != nil {
		return false
	}
	if _, err := protocol.DecodeJavaScriptJSONString(record.Get("content")); err != nil {
		return false
	}
	switch record.String("cause") {
	case "stream_retry", "watchdog", "stream_error", "404_stream_creation":
		return true
	default:
		return false
	}
}
