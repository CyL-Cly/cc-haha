package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) requestTeamPaneSetup(ctx context.Context, scope *cooperative.Scope, prompt teamspawn.PaneSetupPrompt) (string, error) {
	if !scope.BelongsTo(s.process.Owner) {
		return "", cooperative.ErrRestricted
	}
	s.mu.Lock()
	display := s.teamPaneSetupDisplay
	s.teamPaneSetupDisplay = nil
	s.mu.Unlock()
	if display != nil {
		_ = s.emitEvent(tui.Event{Kind: tui.PermissionClosed, Permission: display})
	}
	if prompt.Step == "clear" {
		return "", nil
	}
	request := tui.PermissionRequest{ID: newID(), ToolName: "TeamPaneSetup", Title: prompt.Title, Description: prompt.Description, DisplayOnly: len(prompt.Choices) == 0}
	for _, choice := range prompt.Choices {
		label := choice.Label
		if choice.Description != "" {
			label += " · " + choice.Description
		}
		request.Choices = append(request.Choices, tui.PermissionChoice{Label: label, Decision: tui.PermissionDecision{Allowed: true, UpdatedInput: raw(map[string]string{"choice": choice.Value})}})
	}
	if request.DisplayOnly {
		s.mu.Lock()
		s.teamPaneSetupDisplay = &request
		s.mu.Unlock()
		return "", s.emitEvent(tui.Event{Kind: tui.PermissionAsked, Permission: &request})
	}
	future, err := sdkhost.NewOwnerFuture[tui.PermissionDecision](s.process.Owner)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.permissionOrdinal++
	s.pending[request.ID] = &pendingPermission{request: request, future: future, ordinal: s.permissionOrdinal}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.mu.Unlock()
		_ = s.emitEvent(tui.Event{Kind: tui.PermissionClosed, Permission: &request, Cancelled: ctx.Err() != nil})
	}()
	if err = s.emitEvent(tui.Event{Kind: tui.PermissionAsked, Permission: &request}); err != nil {
		return "", err
	}
	settled := make(chan struct{})
	defer close(settled)
	go func() {
		select {
		case <-ctx.Done():
			_, _ = s.process.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return future.Reject(next, context.Cause(ctx)) })
		case <-settled:
		}
	}()
	decision, err := future.Await(scope)
	if err != nil {
		return "", err
	}
	if !decision.Allowed {
		return "cancelled", nil
	}
	choice := obj(decision.UpdatedInput).String("choice")
	for _, candidate := range prompt.Choices {
		if candidate.Value == choice {
			return choice, nil
		}
	}
	return "", cooperative.ErrRestricted
}
