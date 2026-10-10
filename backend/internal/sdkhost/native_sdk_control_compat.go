package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// Original controlMessageCompat mutates camelCase requestId only when the
// canonical own key is absent. It preserves all unrelated protocol properties.
func normalizeNativeSDKControlKeys(message *protocol.JSONObject) {
	if message.Get("requestId") != nil && message.Get("request_id") == nil {
		message.Set("request_id", message.Get("requestId"))
		message.Delete("requestId")
	}
	if response, ok := message.Object("response"); ok {
		if response.Get("requestId") != nil && response.Get("request_id") == nil {
			response.Set("request_id", response.Get("requestId"))
			response.Delete("requestId")
			value, _ := response.MarshalJSON()
			message.Set("response", value)
		}
	}
}
