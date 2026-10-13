package clibootstrap

import (
	"context"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) verifyNativeAutoGateOnOwner(scope *cooperative.Scope) error {
	if m.Host == nil || !m.options.Native.NativeProcess || m.agentChild || m.teamWorker != nil {
		return nil
	}
	before, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return err
	}
	notice, err := m.Host.VerifyNativeAutoGate(scope, m.nativeAutoModeFlag)
	if err != nil {
		return err
	}
	if notice != "" {
		_, _ = io.WriteString(m.options.Error, notice+"\n")
		m.environmentHookNotice(notice, true)
	}
	after, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return err
	}
	if before.String("mode") != after.String("mode") && m.options.OnPermissionModeChanged != nil {
		return m.options.OnPermissionModeChanged(context.Background(), after.String("mode"))
	}
	return nil
}
