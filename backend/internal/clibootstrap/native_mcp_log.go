package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) logNativeMCPOnOwner(scope *cooperative.Scope, name, message string, failed bool) error {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if parent := m.nativeHelperParent(); parent != nil {
		_, err := nativeAgentOwnerCall(m, scope, parent, func(next *cooperative.Scope) (bool, error) {
			return true, parent.logNativeMCPOnOwner(next, name, message, failed)
		})
		return err
	}
	if err := m.initializeNativeErrorLog(); err != nil {
		return err
	}
	if m.errorLogSink == nil {
		return nil
	}
	if failed {
		return m.errorLogSink.MCPErrorMessage(name, message)
	}
	return m.errorLogSink.MCPDebug(name, message)
}

// mcpauth's native HTTP/provider workers call this observer. Only the immutable
// server/message crosses to the existing process sink; metadata and files are
// sampled on its owner, including when the worker belongs to an Agent view.
func (m *MainPrint) reportNativeMCPDebug(name, message string) {
	target := m
	for parent := target.nativeHelperParent(); parent != nil; parent = target.nativeHelperParent() {
		target = parent
	}
	receipt, err := target.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		return target.logNativeMCPOnOwner(scope, name, message, false)
	})
	if err == nil {
		_ = receipt.Wait()
	}
}
