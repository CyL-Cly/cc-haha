package builtintools

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type SessionCollaborationBindings struct {
	Call func(context.Context, string, protocol.JSONObject) (protocol.JSONObject, error)
	UUID func() string
}

func sessionTool(name string) bool { return toolschema.SessionCollaborationAction(name) != "" }
func (s *Service) HasSessionCollaborationBindings() bool {
	b := s.options.SessionCollaboration
	return b != nil && b.Call != nil && b.UUID != nil
}
func (s *Service) startSessionCollaboration(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasSessionCollaborationBindings() || !sessionTool(name) {
		return nil, capability("actual authenticated desktop collaboration bridge")
	}
	payload := in.Clone()
	key := ""
	if name == "CreateSession" {
		key = "requestId"
	} else if name == "SendSessionMessage" {
		key = "messageId"
	}
	if key != "" && (payload.Get(key) == nil || string(payload.Get(key)) == "null") {
		id := call.ToolUseID
		if id == nil || string(id) == "null" {
			id = quote(s.options.SessionCollaboration.UUID())
		}
		payload.Set(key, id)
	}
	return native(s, scope, func() (queryloop.ToolCallResult, error) {
		result, e := s.options.SessionCollaboration.Call(ctx, toolschema.SessionCollaborationAction(name), payload)
		return queryloop.ToolCallResult{Data: raw(result)}, e
	})
}

func mapSessionCollaboration(data, id json.RawMessage) (protocol.JSONObject, error) {
	encoded, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: quote(string(encoded))}), nil
}

func (s *Service) sessionPermission(scope *cooperative.Scope, name string, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	return s.mcpResourcePermissionDecision(scope, name, in)
}
