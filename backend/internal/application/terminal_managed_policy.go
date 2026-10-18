package application

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) BindManagedPolicyConsent(ctx context.Context, emit tui.Emit) ([]*tui.PermissionRequest, func(), error) {
	if ctx == nil || emit == nil {
		return nil, nil, errors.New("managed settings consent requires a terminal consumer")
	}
	initial, remove := s.process.SubscribeManagedPolicyConsent(func(event clibootstrap.NativeManagedPolicyConsentEvent) {
		request := tui.ManagedPolicyPermission(event.Consent.ID, event.Consent.Settings)
		kind := tui.PermissionAsked
		if event.Closed {
			kind = tui.PermissionClosed
		}
		_ = emit(tui.Event{Kind: kind, Permission: request})
	})
	requests := make([]*tui.PermissionRequest, 0, len(initial))
	for _, consent := range initial {
		requests = append(requests, tui.ManagedPolicyPermission(consent.ID, consent.Settings))
	}
	return requests, remove, nil
}
func (s *Session) respondManagedPolicyConsent(ctx context.Context, id string, decision tui.PermissionDecision) (bool, error) {
	handled, err := s.process.RespondManagedPolicyConsent(ctx, id, decision.Allowed)
	if errors.Is(err, clibootstrap.ErrManagedPolicyRejected) {
		err = tui.ErrManagedPolicyConsentDeclined
	}
	return handled, err
}
