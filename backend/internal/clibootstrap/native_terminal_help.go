package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) TerminalHelpVersion() string { return m.options.Version }

func (m *MainPrint) TerminalHelpCommands(ctx context.Context) ([]protocol.JSONObject, error) {
	var rows []protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Registry == nil || !m.options.Interactive || m.agentChild {
			return cooperative.ErrRestricted
		}
		rows = m.Registry.TerminalHelpCommands()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return rows, err
}
