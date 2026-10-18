package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativeRewindCommand(ctx context.Context, scope *cooperative.Scope) error {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
		return cooperative.ErrRestricted
	}
	if m.options.OpenTerminalMessageSelector == nil {
		return nil
	}
	snapshot, err := m.terminalMessageSelectorOnOwner(ctx, scope)
	if err != nil {
		return err
	}
	return m.options.OpenTerminalMessageSelector(snapshot)
}
