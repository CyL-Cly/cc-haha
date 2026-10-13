package clibootstrap

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// StopAllBackgroundTasks uses the same cancellation and join producer as
// native shutdown. A desktop Stop leaves the canonical owner available for the
// next user turn, while the canceled shells and Agent children are fully joined.
func (m *MainPrint) StopAllBackgroundTasks(ctx context.Context) error {
	receipt, err := m.Owner.SubmitIndependent(context.WithoutCancel(ctx), cooperative.Cancel, func(scope *cooperative.Scope) error {
		if m.closed || m.nativeTools == nil {
			return cooperative.ErrClosed
		}
		pauseErr := m.pauseNativeInProcessTeammatesOnOwner(scope)
		dreamErr := m.retireNativeAutoDreamOnOwner(scope)
		future, err := m.nativeTools.StartBackgroundCleanup(scope)
		if err != nil {
			return errors.Join(pauseErr, dreamErr, err)
		}
		_, err = future.Await(scope)
		return errors.Join(pauseErr, dreamErr, err)
	})
	if err != nil {
		return err
	}
	select {
	case <-receipt.Done():
		return receipt.Wait()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
