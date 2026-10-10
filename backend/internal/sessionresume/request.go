package sessionresume

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// AppendUser models the original next prompt's actual factory construction.
// It does not perform CLI interruption requeue, hooks, or restore state.
func (reducer *Reducer) AppendUser(result Result, content json.RawMessage) (Result, error) {
	if !json.Valid(content) {
		return Result{}, errors.New("next prompt requires original valid JSON content")
	}
	generated := reducer.options.Factory.CreateUser(storage.Object{"content": content})
	var api storage.Object
	_ = json.Unmarshal(generated["message"], &api)
	orderedAPI := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: api["role"]}, protocol.JSONField{Name: "content", Value: api["content"]})
	apiRaw, _ := orderedAPI.MarshalJSON()
	ordered := protocol.NewJSONObject()
	for _, name := range []string{"type", "message", "isMeta", "isVisibleInTranscriptOnly", "isVirtual", "isCompactSummary", "summarizeMetadata", "uuid", "timestamp", "toolUseResult", "mcpMeta", "imagePasteIds", "sourceToolAssistantUUID", "permissionMode", "origin"} {
		raw := generated[name]
		if name == "message" {
			raw = apiRaw
		}
		if raw != nil {
			ordered.Set(name, raw)
		}
	}
	raw, _ := ordered.MarshalJSON()
	item, err := parse(raw)
	if err != nil {
		return Result{}, err
	}
	result.Messages = append(append([]*Message{}, result.Messages...), &Message{item})
	return result, nil
}

// BuildRequest directly delegates independently recovered envelopes to the
// real request builder. It never silently projects or drops an unsupported
// attachment, changes model policy, or accepts an alternate prepared history.
func BuildRequest(result Result, input requestcontext.Context, dependencies requestcontext.Dependencies) (*requestcontext.Result, error) {
	if input.History != nil {
		return nil, errors.New("resume request forbids alternate prepared history")
	}
	input.History = make([]protocol.JSONObject, 0, len(result.Messages))
	for _, message := range result.Messages {
		var record protocol.JSONObject
		if err := json.Unmarshal(encoded(message.value), &record); err != nil || !record.IsObject() {
			return nil, &requestcontext.CapabilityError{Boundary: "non-object resumed envelope"}
		}
		input.History = append(input.History, record)
	}
	return requestcontext.Build(input, dependencies)
}
