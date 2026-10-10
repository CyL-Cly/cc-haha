package clibootstrap

import (
	"context"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Mirrors createCombinedAbortSignal: the actual owner arms/clears the one
// timeout, caller abort delivery cancels the combined signal, and finally
// removes that listener. Physical workers do not execute its policy callback.
func (m *MainPrint) nativeCombinedImageSignal(scope *cooperative.Scope, parent context.Context, delay time.Duration) (context.Context, func() error, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, nil, cooperative.ErrRestricted
	}
	combined, cancel := context.WithCancel(context.WithoutCancel(parent))
	if parent.Err() != nil {
		cancel()
		return combined, func() error { return nil }, nil
	}
	ticket, err := scope.ArmTimer(delay, cancel)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	active := true
	stop := context.AfterFunc(parent, func() {
		_, _ = m.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error {
			if active {
				if err := next.ClearTimer(ticket); err != nil {
					return err
				}
				cancel()
			}
			return nil
		})
	})
	return combined, func() error {
		active = false
		stop()
		return scope.ClearTimer(ticket)
	}, nil
}
