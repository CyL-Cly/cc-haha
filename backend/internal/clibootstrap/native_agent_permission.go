package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type nativeAgentPermissionResolver func(*MainPrint, context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error)

// Parent permission UI owns its original pending request and decision Future.
// Only immutable descriptor/input/identity data crosses Owners; the result
// restores the child's original input pointer and explicit user replacement.
func (m *MainPrint) independentAgentPermissionResolver() nativeAgentPermissionResolver {
	parentResolver := m.options.ResolvePermission
	if parentResolver == nil {
		return nil
	}
	return func(child *MainPrint, ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
		if in == nil || !scope.BelongsTo(child.Owner) {
			return queryloop.ToolPermission{}, cooperative.ErrRestricted
		}
		input := in.Clone()
		descriptor := queryloop.RegisteredTool{Name: tool.Name, Aliases: append([]string(nil), tool.Aliases...), IsMCP: tool.IsMCP, SearchHint: append(json.RawMessage(nil), tool.SearchHint...), AlwaysLoad: tool.AlwaysLoad, ShouldDefer: tool.ShouldDefer}
		agentType := ""
		if child.options.DetachedAgent != nil {
			agentType = child.options.DetachedAgent.AgentType
		}
		request := queryloop.JSONToolCallContext{NativePermissionToolNames: append([]string{}, call.NativePermissionToolNames...), NativePermissionAgentType: agentType, NativePermissionHookSignal: call.NativePermissionHookSignal, CancelModel: call.CancelModel, ToolUseID: append(json.RawMessage(nil), call.ToolUseID...), UserModified: call.UserModified, Opaque: &builtinwrite.CallContext{AgentID: child.options.AgentID}}
		if call.PermDecision != nil {
			copy := cloneNativeAgentPermission(*call.PermDecision)
			copy.Input = &input
			request.PermDecision = &copy
		}
		decision, e := nativeAgentOwnerCall(child, scope, m, func(parentScope *cooperative.Scope) (queryloop.ToolPermission, error) {
			selected, e := parentResolver(ctx, parentScope, descriptor, &input, request)
			return cloneNativeAgentPermission(selected), e
		})
		decision.Input = in
		return decision, e
	}
}
func cloneNativeAgentPermission(value queryloop.ToolPermission) queryloop.ToolPermission {
	copy := value
	copy.Input = nil
	if value.UpdatedInput != nil {
		input := value.UpdatedInput.Clone()
		copy.UpdatedInput = &input
	}
	clone := func(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }
	copy.Message = clone(value.Message)
	copy.AcceptFeedback = clone(value.AcceptFeedback)
	copy.Description = clone(value.Description)
	copy.Suggestions = clone(value.Suggestions)
	copy.DecisionReason = clone(value.DecisionReason)
	copy.Metadata = clone(value.Metadata)
	copy.DisplayName = clone(value.DisplayName)
	copy.UpdatedPermissions = clone(value.UpdatedPermissions)
	copy.ContentBlocks = make([]json.RawMessage, len(value.ContentBlocks))
	for i, block := range value.ContentBlocks {
		copy.ContentBlocks[i] = clone(block)
	}
	return copy
}
func (m *MainPrint) nativeAgentPermissionResolver(fallback func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error)) func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if m.teamWorker != nil && m.teamWorker.pane != nil {
		return m.nativePanePermission
	}
	if d := m.options.DetachedAgent; d != nil && d.resolvePermission != nil {
		return func(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			return d.resolvePermission(m, ctx, scope, tool, in, call)
		}
	}
	return fallback
}
