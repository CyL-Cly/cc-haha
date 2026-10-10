package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"golang.org/x/text/unicode/norm"
)

func (m *MainPrint) nativeBashCwdBindings() *builtintools.BashCwdBindings {
	if !m.options.Native.NativeProcess {
		return nil
	}
	return &builtintools.BashCwdBindings{Owner: m.Owner, TemporaryDirectory: m.options.Native.Temp, Changed: m.nativeBashCwdChanged, PreventChanges: m.options.AgentID != ""}
}

func (m *MainPrint) nativeBashCwdChanged(scope *cooperative.Scope, oldCwd, newCwd string) error {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	newCwd = norm.NFC.String(newCwd)
	if oldCwd == newCwd {
		return nil
	}
	if err := m.state.SetCWD(scope, newCwd); err != nil {
		return err
	}
	if err := m.nativeTools.SetWorkingDirectory(scope, newCwd); err != nil {
		return err
	}
	m.hookEnvironment.Invalidate()
	binding, err := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if err != nil {
		return err
	}
	ref, err := binding.Acquire(scope)
	if err != nil {
		return err
	}
	receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
		defer ref.DeliveryFinished()
		return m.nativeCwdChanged(next, oldCwd, newCwd)
	})
	if err != nil {
		ref.DeliveryFinished()
		return err
	}
	ref.ReclaimRejectedReceipt(receipt)
	return nil
}
