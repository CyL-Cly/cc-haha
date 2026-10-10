package queryloop

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func (batch *SerialToolBatch) structuredAttachment(data json.RawMessage) (*Message, error) {
	if data == nil {
		return nil, nil
	}
	payload, e := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("structured_output")}, protocol.JSONField{Name: "data", Value: data}).MarshalJSON()
	if e != nil {
		return nil, e
	}
	message := Message{Record: batch.factory.CreateAttachment(payload)}
	return &message, nil
}

// The original checkPermissionsAndCallTool catch returns a fresh result list:
// failed call/map/post stages discard any previously allocated success output.
func (batch *SerialToolBatch) innerCallFailureFeedback(use Invocation, id json.RawMessage, failure error, emit func(Message) error) error {
	content := toolerrors.FormatError(failure)
	receipt, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), content}, "")
	if e != nil {
		return e
	}
	block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "is_error", Value: []byte("true")}, protocol.JSONField{Name: "tool_use_id", Value: id})
	blocks, e := json.Marshal([]protocol.JSONObject{block})
	if e != nil {
		return e
	}
	return emit(CreateOrderedUserMessage(batch.factory, storage.Object{"content": blocks, "toolUseResult": receipt, "sourceToolAssistantUUID": quoted(use.Assistant.UUID)}))
}
