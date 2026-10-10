package queryloop

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original catch collects Failure hook messages before allocating the
// error feedback, then returns feedback followed by that retained list.
func (batch *SerialToolBatch) failureEffects(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, failure error, call JSONToolCallContext) ([]Message, error) {
	if batch.capabilities.JSON == nil || batch.capabilities.JSON.FailureToolEffects == nil {
		return nil, nil
	}
	return batch.capabilities.JSON.FailureToolEffects(ctx, tool, input, failure, copyJSONCallContext(call))
}
func (batch *SerialToolBatch) innerFailureWithHooks(ctx context.Context, use Invocation, id json.RawMessage, tool RegisteredTool, input *protocol.JSONObject, failure error, call JSONToolCallContext, emit func(Message) error) error {
	messages, err := batch.failureEffects(ctx, tool, input, failure, call)
	if err != nil {
		return err
	}
	if err = batch.innerCallFailureFeedback(use, id, failure, emit); err != nil {
		return err
	}
	for _, message := range messages {
		if err = emit(message); err != nil {
			return err
		}
	}
	return batch.progress(id, false)
}
