package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// The synthetic Source request has no tool-turn abort signal. It stays in the
// same pending map after a socket/command disconnect and settles only on the
// host's response or the owning sandbox/SDK lifetime ending. Its callback uses
// only behavior, so ordinary tool permissionUpdates/updatedInput are ignored.
func (s *Session) startSandboxNetworkPermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (*sdkhost.OwnerFuture[bool], error) {
	if s.process == nil || scope == nil || !scope.BelongsTo(s.process.Owner) || tool.Name != "SandboxNetworkAccess" || input == nil || len(input.OwnProperties()) != 1 || input.Get("host") == nil {
		return nil, cooperative.ErrRestricted
	}
	future, e := sdkhost.NewOwnerFuture[tui.PermissionDecision](s.process.Owner)
	if e != nil {
		return nil, e
	}
	output, e := sdkhost.NewOwnerFuture[bool](s.process.Owner)
	if e != nil {
		return nil, e
	}
	data, e := input.MarshalJSON()
	if e != nil {
		return nil, e
	}
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "toolUseId", Value: call.ToolUseID})
	if call.PermDecision != nil && call.PermDecision.Description != nil {
		payload.Set("description", call.PermDecision.Description)
	}
	_, permissions, e := s.process.Controller.SDKSettingsOnOwner(scope)
	if e != nil {
		return nil, e
	}
	request := tui.PermissionRequest{ID: newID(), ToolName: tool.Name, Input: data, Payload: raw(payload), PermissionMode: permissions.String("mode"), Description: payload.String("description")}
	s.mu.Lock()
	s.permissionOrdinal++
	s.pending[request.ID] = &pendingPermission{request: request, future: future, ordinal: s.permissionOrdinal}
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		_, _ = s.process.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return future.Reject(next, ctx.Err()) })
	})
	_, e = future.Then(scope, func(next *cooperative.Scope, decision tui.PermissionDecision, failure error) error {
		stop()
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.mu.Unlock()
		closed := request
		payload := obj(request.Payload)
		allowed := failure == nil && decision.Allowed
		payload.Set("allowed", raw(allowed))
		closed.Payload = payloadJSON(payload)
		_ = s.emitEvent(tui.Event{Kind: tui.PermissionClosed, Permission: &closed, Cancelled: failure != nil})
		return output.Resolve(next, allowed)
	})
	if e != nil {
		stop()
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.mu.Unlock()
		return nil, e
	}
	if e = s.emitEvent(tui.Event{Kind: tui.PermissionAsked, Permission: &request, ToolName: tool.Name}); e != nil {
		_ = future.Reject(scope, e)
	}
	if ctx.Err() != nil {
		_ = future.Reject(scope, ctx.Err())
	}
	return output, nil
}
