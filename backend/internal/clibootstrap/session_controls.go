package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) SetSessionPermissionMode(ctx context.Context, mode string) error {
	if m.Host == nil || m.closed {
		return cooperative.ErrClosed
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return m.Host.SetNativePermissionMode(scope, mode) })
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func (m *MainPrint) AppendSessionMetadata(ctx context.Context, metadata protocol.JSONObject) error {
	if m.Controller == nil || m.closed {
		return cooperative.ErrClosed
	}
	metadata.Set("type", quoteMain("session-meta"))
	metadata.Set("isMeta", []byte("true"))
	metadata.Set("workDir", quoteMain(m.options.Native.CWD))
	metadata.Set("timestamp", quoteMain(m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return m.Controller.AppendRuntimeMetadata(scope, metadata) })
	if err != nil {
		return err
	}
	return receipt.Wait()
}
