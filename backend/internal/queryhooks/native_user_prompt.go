package queryhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeUserPromptInput struct {
	Prompt, PermissionMode json.RawMessage
	Call                   Context
}

func (s *Service) ExecuteUserPromptSubmitHooks(ctx context.Context, in NativeUserPromptInput, consume func(EventResult) error) error {
	if s == nil || consume == nil {
		return errors.New("user prompt Hook requires actual service and consumer")
	}
	sid := in.Call.AgentID
	if sid == "" {
		sid = s.mainSessionID()
	}
	present, err := s.NativeEventPresent("UserPromptSubmit", sid)
	if err != nil || !present {
		return err
	}
	if _, err := protocol.DecodeJavaScriptJSONString(in.Prompt); err != nil {
		return err
	}
	id := s.options.UUID()
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: in.PermissionMode}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "prompt", Value: in.Prompt})
	return s.ExecuteCommandEvent(ctx, CommandEvent{Event: "UserPromptSubmit", SessionID: sid, ToolUseID: id, Fields: fields, Call: in.Call}, func(value EventResult) error {
		return s.expandNativeTaskResult("UserPromptSubmit", id, value, consume)
	})
}
