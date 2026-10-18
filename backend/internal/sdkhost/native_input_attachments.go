package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Native input uses the same getAttachmentMessages/toArray async-generator
// consumer before processTextPrompt creates its user message. The real producer
// runs once; an empty SIMPLE list still owns its terminal next() reaction.
func (h *Host) nativeInitialInputAttachments(scope *cooperative.Scope, content json.RawMessage, skip bool) ([]protocol.JSONObject, bool, error) {
	if !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || h.inputModelContext == nil || h.options.Print.InputAttachments == nil && h.options.Print.PlanAttachments == nil {
		return nil, false, cap("actual owned input attachment producer")
	}
	if skip {
		return nil, true, nil
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: content})
	input := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: raw(message)})
	text, present, err := originalInputAttachmentText(&input)
	if err != nil || !present {
		return nil, false, err
	}
	previous := h.serial.History()
	var attachments []queryloop.Message
	if h.options.Print.InputAttachments != nil {
		attachments, err = h.options.Print.InputAttachments(h.inputModelContext, scope, text, previous)
	} else if h.options.Print.PlanAttachments != nil {
		attachments, err = h.options.Print.PlanAttachments(scope, previous)
	}
	if err != nil {
		return nil, false, err
	}
	produced, err := NewOwnerFuture[[]queryloop.Message](h.options.Owner)
	if err != nil {
		return nil, false, err
	}
	if err = produced.Resolve(scope, attachments); err != nil {
		return nil, false, err
	}
	index := 0
	var yield GeneratorContinuation[queryloop.Message]
	yield = func(next *cooperative.Scope, iterator *OwnerIterator[queryloop.Message], request IteratorRequest[queryloop.Message]) error {
		if request.Returning {
			return iterator.CompleteReturn(next)
		}
		if index == len(attachments) {
			if len(attachments) == 0 {
				// getAttachmentMessages has an explicit `return` on its empty
				// branch; AsyncGeneratorBody awaits that undefined return value.
				return iterator.CompleteDefined(next, queryloop.Message{}, false)
			}
			return iterator.Complete(next)
		}
		value := attachments[index]
		index++
		return iterator.Yield(next, value, yield)
	}
	generator, err := NewOwnerIterator(h.options.Owner, func(next *cooperative.Scope, iterator *OwnerIterator[queryloop.Message], request IteratorRequest[queryloop.Message]) error {
		if request.Returning {
			return iterator.CompleteReturn(next)
		}
		// getAttachmentMessages awaits getAttachments, including its already
		// fulfilled empty gate, before deciding whether there is a first yield.
		return AwaitGenerator(next, iterator, produced, func(next *cooperative.Scope, _ []queryloop.Message) error { return yield(next, iterator, request) }, nil)
	})
	if err != nil {
		return nil, false, err
	}
	values := []protocol.JSONObject{}
	for {
		next, err := generator.Next(scope)
		if err != nil {
			return nil, false, err
		}
		value, err := next.Await(scope)
		if err != nil {
			return nil, false, err
		}
		if value.Done {
			break
		}
		if value.Value.Type() != "attachment" {
			return nil, false, cap("original input attachment producer")
		}
		values = append(values, value.Value.Record)
	}
	// processUserInputBase awaits the toArray async function's return.
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, false, err
	}
	return values, true, nil
}
