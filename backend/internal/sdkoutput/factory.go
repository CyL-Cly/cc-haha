package sdkoutput

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// User uses the shared real factory and its existing ordered user adapter.
func User(factory *messages.Factory, options storage.Object) (Message, error) {
	if factory == nil {
		return Message{}, cap("actual user factory")
	}
	record := messages.OrderedUser(factory.CreateUser(options))
	raw, err := record.MarshalJSON()
	if err != nil {
		return Message{}, err
	}
	return Decode(raw)
}

// Assistant keeps the Source field slots around values independently produced
// by the real shared factory. No factory defaults are duplicated here.
func Assistant(factory *messages.Factory, options storage.Object, apiError bool) (Message, error) {
	if factory == nil {
		return Message{}, cap("actual assistant factory")
	}
	content := options["content"]
	if apiError && !isString(content) {
		return Message{}, cap("API error original string content")
	}
	if !isString(content) {
		var blocks []json.RawMessage
		if len(bytes.TrimSpace(content)) == 0 || bytes.TrimSpace(content)[0] != '[' || json.Unmarshal(content, &blocks) != nil {
			return Message{}, cap("assistant original string/array content")
		}
	}
	var value storage.Object
	if apiError {
		value = factory.CreateAssistantAPIError(options)
	} else {
		value = factory.CreateAssistant(options)
	}
	var unordered protocol.JSONObject
	_ = unordered.UnmarshalJSON(value["message"])
	api := protocol.NewJSONObject()
	for _, key := range []string{"id", "container", "model", "role", "stop_reason", "stop_sequence", "type", "usage", "content", "context_management"} {
		api.Set(key, unordered.Get(key))
	}
	raw, err := api.MarshalJSON()
	if err != nil {
		return Message{}, err
	}
	record := protocol.NewJSONObject()
	for _, key := range []string{"type", "uuid", "timestamp", "message", "requestId", "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel", "isApiErrorMessage", "isVirtual"} {
		if key == "message" {
			record.Set(key, raw)
		} else {
			record.Set(key, value[key])
		}
	}
	raw, err = record.MarshalJSON()
	if err != nil {
		return Message{}, err
	}
	return Decode(raw)
}
