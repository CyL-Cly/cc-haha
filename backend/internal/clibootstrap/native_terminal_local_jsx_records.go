package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) CompleteTerminalLocalJSXRecords(ctx context.Context, name, args string, output json.RawMessage, display string, shouldQuery bool) ([]protocol.JSONObject, error) {
	var records []protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		paneHelp := name == "help" && m.teamWorker != nil && m.teamWorker.pane != nil
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil && !paneHelp {
			return cooperative.ErrRestricted
		}
		var err error
		if name == "help" && display == "system" && !shouldQuery {
			var message string
			if json.Unmarshal(output, &message) == nil && message == "Help dialog dismissed" {
				fullscreen, e := m.terminalFullscreenOnOwner(ctx, scope)
				if e != nil {
					return e
				}
				if fullscreen {
					display = "skip"
				}
			}
		}
		records, err = m.Host.CompleteTerminalLocalJSXRecordsJSONOnOwner(scope, name, args, output, display, shouldQuery)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return records, err
}
