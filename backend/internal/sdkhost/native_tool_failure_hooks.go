package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func (h *Host) nativeFailureToolHooks(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, failure error, call queryloop.JSONToolCallContext, makeContext func(queryloop.Round) queryhooks.Context) ([]queryloop.Message, error) {
	messages := []queryloop.Message{}
	c := h.nativeToolHookContext(scope, call, makeContext)
	sid := h.options.Controller.SessionID()
	if c.AgentID != "" {
		sid = c.AgentID
	}
	has, err := h.hooks.NativeEventPresent("PostToolUseFailure", sid)
	if err != nil || !has {
		return messages, err
	}
	id, err := nativeHookToolID(call.ToolUseID)
	if err != nil {
		return messages, err
	}
	name := "PostToolUseFailure:" + tool.Name
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quote(tool.Name)}, protocol.JSONField{Name: "tool_input", Value: raw(*in)}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID}, protocol.JSONField{Name: "error", Value: toolerrors.FormatError(failure)}, protocol.JSONField{Name: "is_interrupt", Value: raw(errors.Is(failure, context.Canceled))})
	err = h.hooks.ExecuteCommandEvent(ctx, queryhooks.CommandEvent{Event: "PostToolUseFailure", SessionID: sid, ToolUseID: id, MatchQuery: tool.Name, Fields: fields, Call: c, IfMatcher: nativeHookIfMatcher(tool, in)}, func(value queryhooks.EventResult) error {
		if value.Message != nil {
			if value.Message.Type() == "attachment" {
				a, _ := value.Message.Record.Object("attachment")
				if a.String("type") == "hook_cancelled" {
					messages = append(messages, h.nativeHookAttachment("hook_cancelled", "PostToolUseFailure", name, call.ToolUseID))
					return nil
				}
				if a.String("type") != "hook_blocking_error" {
					messages = append(messages, *value.Message)
				}
			} else {
				messages = append(messages, *value.Message)
			}
		}
		f := value.Fields
		if blocked, ok := f.Object("blockingError"); ok {
			// executeHooks emits preventContinuation's blockingError first,
			// then the independent blockingError result. The Failure facade
			// preserves both; unlike PostToolUse it does not stop that stream.
			if hookRawTruthy(f.Get("preventContinuation")) {
				messages = append(messages, h.nativeHookAttachment("hook_blocking_error", "PostToolUseFailure", name, call.ToolUseID, protocol.JSONField{Name: "blockingError", Value: raw(blocked)}))
			}
			messages = append(messages, h.nativeHookAttachment("hook_blocking_error", "PostToolUseFailure", name, call.ToolUseID, protocol.JSONField{Name: "blockingError", Value: raw(blocked)}))
		}
		if text := f.Get("systemMessage"); hookRawTruthy(text) {
			messages = append(messages, h.nativeHookAttachment("hook_system_message", "PostToolUseFailure", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: text}))
		}
		if text := f.Get("additionalContext"); hookRawTruthy(text) {
			messages = append(messages, h.nativeHookAttachment("hook_additional_context", "PostToolUseFailure", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: raw([]json.RawMessage{text})}))
		}
		return nil
	})
	return messages, err
}
