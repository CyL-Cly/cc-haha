package messages

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func IsNativeModelFallbackRecord(record protocol.JSONObject) bool {
	return record.String("type") == "system" && record.String("subtype") == "informational" && record.String("level") == "warning" && string(record.Get("isMeta")) == "false" && record.String("uuid") != "" && record.String("timestamp") != "" && strings.HasPrefix(record.String("content"), "Switched to ") && strings.Contains(record.String("content"), " due to high demand for ")
}
