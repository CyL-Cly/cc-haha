package messages

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// CreateOrderedAssistantAPIError keeps the Source factory's property slots and
// the existing Factory's UUID -> Date -> message-ID allocations. This additive
// seam accepts the actual presentation function's string/scalar metadata domain;
// invalid inputs fail before the shared allocator advances.
func (factory *Factory) CreateOrderedAssistantAPIError(options storage.Object) (protocol.JSONObject, error) {
	if factory == nil {
		return protocol.JSONObject{}, errors.New("ordered API error requires a shared message factory")
	}
	if _, err := protocol.DecodeJavaScriptJSONString(options["content"]); err != nil {
		return protocol.JSONObject{}, errors.New("ordered API error requires original string content")
	}
	for _, key := range []string{"apiError", "error", "errorDetails", "businessErrorCode", "sourceModel"} {
		value := options[key]
		if value == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			return protocol.JSONObject{}, errors.New("ordered API error requires string or null metadata: " + key)
		}
	}
	value := factory.CreateAssistantAPIError(options)
	api, _, err := storage.NormalizeObject(value["message"])
	if err != nil {
		return protocol.JSONObject{}, err
	}
	message := protocol.NewJSONObject()
	for _, key := range []string{"id", "container", "model", "role", "stop_reason", "stop_sequence", "type", "usage", "content", "context_management"} {
		message.Set(key, api[key])
	}
	raw, err := message.MarshalJSON()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	record := protocol.NewJSONObject()
	for _, key := range []string{"type", "uuid", "timestamp", "message", "requestId", "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel", "isApiErrorMessage", "isVirtual"} {
		if key == "message" {
			record.Set(key, json.RawMessage(raw))
		} else {
			record.Set(key, value[key])
		}
	}
	return record, nil
}
