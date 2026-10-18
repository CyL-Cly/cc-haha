package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) TerminalBtwQuestion(ctx context.Context, id, question string) (protocol.JSONObject, error) {
	var future *sdkhost.OwnerFuture[protocol.JSONObject]
	var done <-chan struct{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		var err error
		future, done, err = m.Host.BeginTerminalBtw(scope, ctx, id, question)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value, err := future.Wait(ctx)
	<-done
	return value, err
}

func (m *MainPrint) CompleteTerminalBtwUsage(ctx context.Context, args string, fullscreen bool) ([]protocol.JSONObject, bool, error) {
	var records []protocol.JSONObject
	var immediate bool
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		var err error
		records, immediate, err = m.Host.CompleteTerminalBtwUsage(scope, args, fullscreen)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return records, immediate, err
}
