package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) clearNativeMCPForTerminalOnOwner(_ context.Context, scope *cooperative.Scope) error {
	if scope == nil || !scope.BelongsTo(m.Owner) || !m.options.Native.NativeProcess || !m.options.Interactive || m.mcpBorrowed || m.agentChild || m.shuttingDown || m.closed {
		return cooperative.ErrRestricted
	}
	if m.mcpTools == nil {
		return nil
	}
	return m.mcpTools.ClearInteractiveCatalog(scope, m.publishNativeMCPCatalog)
}
