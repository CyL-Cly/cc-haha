package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalMCPParsingDiagnostics is called once when the actual local MCP
// dialog mounts. The returned snapshot remains stable while peers reconnect.
func (m *MainPrint) TerminalMCPParsingDiagnostics(ctx context.Context) ([]protocol.JSONObject, error) {
	if m == nil || m.Owner == nil {
		return nil, cooperative.ErrRestricted
	}
	var result []protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) || m.closed || m.shuttingDown {
			return cooperative.ErrClosed
		}
		if m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.mcpConfigurations == nil || m.Global == nil {
			return mainCap("actual native MCP parsing diagnostics producer")
		}
		var err error
		result, err = m.mcpConfigurations.Diagnostics(ctx, m.Global.Path())
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
