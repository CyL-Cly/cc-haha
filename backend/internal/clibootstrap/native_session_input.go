package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) AdmitNativeSDKInput(ctx context.Context, input protocol.JSONObject, admitted func()) (bool, error) {
	if !m.initialized || m.closed {
		return false, cooperative.ErrClosed
	}
	novel := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		novel, err = m.Host.AdmitSessionInput(scope, input)
		if err == nil && novel && admitted != nil {
			admitted()
		}
		return err
	})
	if err != nil {
		return false, err
	}
	if err = receipt.Wait(); err != nil {
		return false, err
	}
	return novel, nil
}

func (m *MainPrint) RunAdmittedSDKTurn(ctx context.Context) error {
	if !m.initialized || m.closed {
		return cooperative.ErrClosed
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) (err error) {
		if err = m.markNativePaneActive(scope); err != nil {
			return err
		}
		if err = m.nativeRunState(scope, "running"); err != nil {
			return err
		}
		defer func() {
			if !m.shuttingDown {
				if idleErr := m.nativeRunState(scope, "idle"); err == nil {
					err = idleErr
				}
			}
		}()
		if _, err = m.Host.RunDequeued(scope, ctx); err != nil {
			return err
		}
		return m.drainBackgroundTasksOnOwner(scope, ctx)
	})
}
