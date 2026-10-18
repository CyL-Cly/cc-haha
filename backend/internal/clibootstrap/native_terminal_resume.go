package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
)

func (m *MainPrint) LoadTerminalResumeLog(ctx context.Context, log protocol.JSONObject) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || m.Host == nil {
			return cooperative.ErrClosed
		}
		if err := m.Host.CheckTerminalResumeOnOwner(scope); err != nil {
			return err
		}
		future, err := m.Controller.BeginTerminalFullLogOnOwner(scope, log)
		if err != nil {
			return err
		}
		result, err = future.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) TerminalResumeLog(ctx context.Context, log protocol.JSONObject) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.Host == nil {
			return cooperative.ErrRestricted
		}
		if err := m.resumeTerminalLogOnOwner(ctx, scope, log, false); err != nil {
			return err
		}
		messages, err := querypreflight.InspectionRecords(m.Host.History())
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "history", Value: rawMain(messages)})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
