package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) MarkTerminalTasksHint(ctx context.Context) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.closed {
			return cooperative.ErrRestricted
		}
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if nativeSettingsTruthy(current.Get("hasSeenTasksHint")) {
				return current, nil
			}
			value := current.Clone()
			value.Set("hasSeenTasksHint", json.RawMessage(`true`))
			return &value, nil
		})
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
