package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// SetDesktopNetworkEnvironment is the original before-turn SDK environment
// update consumer. Only its network variables are admitted; the current HTTP
// worker retains its immutable request snapshot and the next request sees the
// same Host/owner with the updated transport policy.
func (m *MainPrint) SetDesktopNetworkEnvironment(ctx context.Context, variables map[string]string) error {
	allowed := map[string]bool{"API_TIMEOUT_MS": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "http_proxy": true, "https_proxy": true, "ALL_PROXY": true, "all_proxy": true, "NO_PROXY": true, "no_proxy": true, "CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": true, "CLAUDE_STREAM_MAX_DURATION_MS": true}
	snapshot := map[string]string{}
	for key, value := range variables {
		if !allowed[key] {
			return cooperative.ErrRestricted
		}
		snapshot[key] = value
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || !m.initialized || m.Host == nil {
			return cooperative.ErrClosed
		}
		if m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.NativeDesktopNetworkBound {
			return cooperative.ErrRestricted
		}
		for key, value := range snapshot {
			if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
				return err
			}
		}
		return m.Host.SetNativeRequestEnvironment(scope, m.nativeOpenAIEnvironment())
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}
