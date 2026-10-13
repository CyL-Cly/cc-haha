package clibootstrap

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpheaders"
)

func (m *MainPrint) nativeMCPIngressToken() string {
	if m.options.SessionIngressAuth == nil {
		return ""
	}
	m.options.SessionIngressAuth.UpdateEnvironment(m.nativeOpenAIEnvironment())
	return m.options.SessionIngressAuth.Token()
}

func (m *MainPrint) nativeMCPHeadersPolicy(ctx context.Context) (mcpheaders.Options, error) {
	var options mcpheaders.Options
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown {
			return context.Canceled
		}
		trusted, e := m.nativeGoalTrust(scope)
		if e != nil {
			return e
		}
		options = mcpheaders.Options{NonInteractive: !m.options.Interactive, TrustAccepted: trusted, Environment: m.nativeOpenAIEnvironment(), CWD: m.state.Snapshot().CWD, Run: mcpheaders.NativeRun, Error: func(e error) {
			m.outputMu.Lock()
			defer m.outputMu.Unlock()
			_, _ = fmt.Fprintln(m.options.Error, "MCP headersHelper failed: "+e.Error())
		}}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return options, e
}
