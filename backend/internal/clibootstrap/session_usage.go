package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) ReadSessionUsage(ctx context.Context) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.Host == nil || !m.initialized || m.closed {
			return cooperative.ErrClosed
		}
		var failure error
		result, failure = m.Host.NativeSessionUsage(scope)
		return failure
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
