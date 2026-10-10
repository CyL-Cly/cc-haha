package messages

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// IsNativeAPIRetryRecord recognizes the original createSystemAPIErrorMessage
// envelope. The ASK consumer additionally requires the actual query producer's
// notice marker; external SDK input cannot fabricate that marker.
func IsNativeAPIRetryRecord(record protocol.JSONObject) bool {
	if record.String("type") != "system" || record.String("subtype") != "api_error" || record.String("level") != "error" || record.String("uuid") == "" || record.String("timestamp") == "" {
		return false
	}
	for _, key := range []string{"retryInMs", "retryAttempt", "maxRetries"} {
		value := bytes.TrimSpace(record.Get(key))
		if len(value) == 0 || !json.Valid(value) || value[0] != 'n' && (value[0] < '0' || value[0] > '9') && value[0] != '-' {
			return false
		}
	}
	if _, ok := record.Object("error"); !ok {
		return false
	}
	for _, field := range record.OwnProperties() {
		var name string
		for _, key := range []string{"type", "subtype", "level", "cause", "error", "retryInMs", "retryAttempt", "maxRetries", "timestamp", "uuid"} {
			if bytes.Equal(field.NameJSON, []byte(`"`+key+`"`)) {
				name = key
				break
			}
		}
		if name == "" {
			return false
		}
	}
	return true
}
