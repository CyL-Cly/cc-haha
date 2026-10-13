package sdkhost

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// Actual QueryEngine system-case output. toSDKMessages intentionally excludes
// api_error, so sending this through the ordinary normalizer loses retry UI.
func nativeAPIRetryOutput(message protocol.JSONObject, sessionID string, original *provider.SDKStreamError) protocol.JSONObject {
	failure, _ := message.Object("error")
	status := failure.Get("status")
	if len(status) == 0 || string(status) == "null" {
		status = raw(nil)
	}
	var code int
	_ = json.Unmarshal(status, &code)
	detail := failure.String("message")
	if original != nil {
		detail = original.Message
	}
	category := "unknown"
	switch {
	case code == 529 || strings.Contains(detail, `"type":"overloaded_error"`):
		category = "rate_limit"
	case code == 429:
		category = "rate_limit"
	case code == 401 || code == 403:
		category = "authentication_failed"
	case code >= 408:
		category = "server_error"
	}
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("system")},
		protocol.JSONField{Name: "subtype", Value: quote("api_retry")},
		protocol.JSONField{Name: "attempt", Value: message.Get("retryAttempt")},
		protocol.JSONField{Name: "max_retries", Value: message.Get("maxRetries")},
		protocol.JSONField{Name: "retry_delay_ms", Value: message.Get("retryInMs")},
		protocol.JSONField{Name: "error_status", Value: status},
		protocol.JSONField{Name: "error", Value: quote(category)},
		protocol.JSONField{Name: "session_id", Value: quote(sessionID)},
		protocol.JSONField{Name: "uuid", Value: message.Get("uuid")},
	)
}
