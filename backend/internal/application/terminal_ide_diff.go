package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) mountTerminalIDEDiff(ctx context.Context, scope *cooperative.Scope, request tui.PermissionRequest, input protocol.JSONObject, future *sdkhost.OwnerFuture[tui.PermissionDecision]) func() {
	if !s.terminalInteractive {
		return func() {}
	}
	job, err := s.process.BeginTerminalIDEDiff(ctx, scope, request.ToolName, input, func(next *cooperative.Scope, result clibootstrap.TerminalIDEDiffResult, failure error) error {
		if failure != nil {
			updated := request
			updated.IDEName = ""
			return s.emitEvent(tui.Event{Kind: tui.PermissionUpdated, Permission: &updated})
		}
		decision := tui.PermissionDecision{Allowed: result.Allowed}
		if result.Allowed {
			decision.UpdatedInput = raw(result.Input)
		}
		return future.Resolve(next, decision)
	})
	if err != nil || job == nil {
		return func() {}
	}
	updated := request
	updated.IDEName = job.Name
	_ = s.emitEvent(tui.Event{Kind: tui.PermissionUpdated, Permission: &updated})
	return func() { job.StopOnOwner(scope) }
}
