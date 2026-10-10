package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// Input-stage context is the actual caller's model/abort lifetime, installed
// only around the original SerialASK acceptance. It is never a second model
// loop or an environment-controlled shell flag.
func (h *Host) beginInputContext(ctx context.Context) func() {
	previous := h.inputModelContext
	h.inputModelContext = ctx
	return func() { h.inputModelContext = previous }
}

func (h *Host) processInteractiveQueuedBatch(scope *cooperative.Scope, commands []sessioncontroller.QueuedInput) (*sessioncontroller.ProcessedInput, error) {
	if !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || h.inputModelContext == nil || len(commands) < 2 {
		return nil, cooperative.ErrRestricted
	}
	previous := h.serial.History()
	var combined *sessioncontroller.ProcessedInput
	for i, command := range commands {
		if command.Mode == "bash" {
			return nil, cap("Bash command in interactive batch")
		}
		origin := command.Origin
		if origin == nil && command.Mode == "task-notification" {
			origin = raw(protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote("task-notification")}))
		}
		processed, e := h.processNativeInputWithPromptHooks(scope, command.Content, command.UUID, command.SkipSlash, command.Meta, origin, i > 0)
		if e != nil {
			return nil, e
		}
		if processed == nil || len(processed.Messages) == 0 {
			return nil, cap("empty interactive input producer")
		}
		if i == 0 {
			value := *processed
			value.Messages = append([]protocol.JSONObject(nil), processed.Messages...)
			combined = &value
			// Same executeUserInput contract: first input gets turn attachments; later
			// inputs preserve their own UUID/origin but skip shared context duplication.
			if !value.NoQuery && !value.AttachmentsHandled {
				var attached []queryloop.Message
				if h.options.Print.InputAttachments != nil {
					text, present, e := originalInputAttachmentText(&value.Messages[0])
					if e != nil {
						return nil, e
					}
					if present {
						attached, e = h.options.Print.InputAttachments(h.inputModelContext, scope, text, previous)
						if e != nil {
							return nil, e
						}
					}
				} else if h.options.Print.PlanAttachments != nil {
					attached, e = h.options.Print.PlanAttachments(scope, previous)
					if e != nil {
						return nil, e
					}
				}
				for _, message := range attached {
					if message.Type() != "attachment" {
						return nil, cap("interactive first-input attachment producer")
					}
					combined.Messages = append(combined.Messages, message.Record)
				}
			}
			combined.AttachmentsHandled = true
		} else {
			combined.Messages = append(combined.Messages, processed.Messages...)
		}
	}
	return combined, nil
}
