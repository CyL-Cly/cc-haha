package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) CompleteTerminalLocalJSXJSON(ctx context.Context, name, args string, output json.RawMessage, display string, shouldQuery bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		return m.Host.CompleteTerminalLocalJSXJSONOnOwner(scope, name, args, output, display, shouldQuery)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

func (m *MainPrint) CompleteTerminalLocalJSX(ctx context.Context, name, args string, output *string, display string, shouldQuery bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		return m.Host.CompleteTerminalLocalJSXOnOwner(scope, name, args, output, display, shouldQuery)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
