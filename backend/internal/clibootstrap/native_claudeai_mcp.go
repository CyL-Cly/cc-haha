package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
)

func (m *MainPrint) newNativeClaudeAIMCP() (*CLIClaudeAIMCP, error) {
	// Borrow the actual print owner/settings/global/secure services. No second
	// process, owner, credential store or authentication decision is created.
	command := &MainCommand{version: m.options.Version, pid: m.options.PID, Owner: m.Owner, Native: m.options.Native, Settings: m.nativeSettings, Startup: m.Startup, Global: m.Global, Secure: m.secure, mcpClaudeAuth: m.openAIAuth, NonInteractive: !m.options.Interactive, credentialPlatform: m.options.Platform, credentialDirectory: "/home/claude/.claude/remote", credentialCache: map[string]string{}}
	if m.options.Native.CCRCredentialDirectory != "" {
		command.credentialDirectory = m.options.Native.CCRCredentialDirectory
	}
	// MCP discovery/connection already owns its native admission. Deliver only
	// credential reads/refresh completions on that same owner; never admit an
	// additional ASK stack while its original continuation is active.
	return command.newClaudeAIMCP(context.Background(), nil, func(ctx context.Context, work func(*cooperative.Scope) error) error {
		receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, work)
		if err != nil {
			return err
		}
		return receipt.Wait()
	})
}

func (m *MainPrint) nativeAllMCPConfigurations(ctx context.Context) (mcpconfig.Result, error) {
	return m.nativeCLIAllMCPConfigurations(ctx)
}
