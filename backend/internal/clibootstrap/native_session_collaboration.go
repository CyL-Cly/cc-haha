package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionbridge"
)

func (m *MainPrint) EnqueueSessionMessage(ctx context.Context, input protocol.JSONObject, canSend func() bool) (protocol.JSONObject, error) {
	if !m.initialized {
		return protocol.JSONObject{}, cooperative.ErrClosed
	}
	request := input.Clone()
	var result protocol.JSONObject
	receipt, e := m.Owner.SubmitIndependent(context.WithoutCancel(ctx), cooperative.Input, func(scope *cooperative.Scope) error {
		if canSend != nil && !canSend() {
			return errors.New("Session turn was cancelled before delivery")
		}
		var e error
		result, e = m.Host.AcceptSessionMessage(scope, request)
		return e
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	select {
	case <-receipt.Done():
		return result, receipt.Wait()
	case <-ctx.Done():
		return protocol.JSONObject{}, context.Cause(ctx)
	}
}

func (m *MainPrint) CancelSessionMessages(ctx context.Context) error {
	if !m.initialized {
		return cooperative.ErrClosed
	}
	receipt, e := m.Owner.SubmitIndependent(context.WithoutCancel(ctx), cooperative.Cancel, func(scope *cooperative.Scope) error {
		_, e := m.Controller.CancelSessionInboxOnOwner(scope)
		return e
	})
	if e != nil {
		return e
	}
	select {
	case <-receipt.Done():
		return receipt.Wait()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (m *MainPrint) RunSessionMessages(ctx context.Context) error {
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for {
			ran, e := m.Host.RunDequeued(scope, ctx)
			if e != nil {
				return e
			}
			if !ran {
				return m.drainBackgroundTasksOnOwner(scope, ctx)
			}
		}
	})
}

func (m *MainPrint) SelectedModel() string { return m.options.Arguments.Model }

func (m *MainPrint) nativeSessionCollaborationBindings() *builtintools.SessionCollaborationBindings {
	config, e := sessionbridge.GetConfig(m.options.Native.Environment)
	if e != nil || config == nil {
		return nil
	}
	return &builtintools.SessionCollaborationBindings{Call: config.Call, UUID: uuidMain}
}
