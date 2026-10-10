package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Session) submitHookInitialInput(ctx context.Context) (bool, error) {
	content, err := s.process.TakeInitialUserMessage(ctx)
	if err != nil || content == nil {
		return false, err
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "session_id", Value: raw("")}, protocol.JSONField{Name: "message", Value: append(append([]byte(`{"role":"user","content":`), content...), '}')}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)})
	return true, s.SubmitSDK(ctx, input)
}
