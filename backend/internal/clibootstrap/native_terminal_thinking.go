package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func (m *MainPrint) TerminalThinking(ctx context.Context) (requestcontext.Thinking, bool, error) {
	var value requestcontext.Thinking
	var mid bool
	if m.Host == nil {
		return value, false, cooperative.ErrClosed
	}
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		value, mid, e = m.Host.TerminalThinking(scope)
		return e
	})
	if e != nil {
		return value, false, e
	}
	e = receipt.Wait()
	return value, mid, e
}
func (m *MainPrint) SetTerminalThinking(ctx context.Context, enabled bool) error {
	if m.Host == nil {
		return cooperative.ErrClosed
	}
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return m.Host.SetTerminalThinking(scope, enabled) })
	if e != nil {
		return e
	}
	return receipt.Wait()
}
