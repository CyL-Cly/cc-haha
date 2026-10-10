package builtintools

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// CallTerminalBash uses BashTool.call's actual process/task producer, without
// model permission resolution. The owning caller has checked the original
// sandbox settings before reaching this user-initiated unsandboxed path.
func (s *Service) CallTerminalBash(ctx context.Context, scope *cooperative.Scope, command string, progress func(json.RawMessage, protocol.JSONObject) error) (json.RawMessage, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	in := protocol.NewJSONObject(field("command", command), field("dangerouslyDisableSandbox", true))
	result, e := s.callBash(ctx, in, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, OnProgress: progress})
	return result.Data, e
}
func (s *Service) MapTerminalBashResult(scope *cooperative.Scope, data, id json.RawMessage) (protocol.JSONObject, error) {
	if e := s.check(scope); e != nil {
		return protocol.JSONObject{}, e
	}
	return s.mapBashResult(data, id)
}
