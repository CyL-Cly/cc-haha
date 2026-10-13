package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// The standalone MCP facade shares original Read's counting consumer. It
// borrows this command's native I/O/auth rather than constructing an ASK.
func (m *NativeMCPToolContext) nativeMCPReadTokenCount(_ context.Context, scope *cooperative.Scope, content string) (*float64, error) {
	if !scope.BelongsTo(m.command.Owner) {
		return nil, cooperative.ErrRestricted
	}
	n := m.command.Native
	if m.modelTLS == nil {
		var err error
		m.modelTLS, err = tlsconfig.NewState(n.readFile)
		if err != nil {
			return nil, nil
		}
	}
	auth := m.modelAuth
	if auth == nil {
		auth = &MainPrint{Owner: m.command.Owner, Global: m.command.Global, Startup: m.command.Startup, environment: m.command.Settings.environment, tls: m.modelTLS, commandBinding: m.binding, options: MainPrintOptions{Native: n, Platform: m.platform, PID: m.command.pid}}
	}
	if auth.options.thinkingSummaryState == nil {
		auth.options.thinkingSummaryState = &nativeThinkingSummaryState{values: map[string]bool{}}
	}
	env := auth.nativeOpenAIEnvironment()
	if envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) {
		return auth.nativeBedrockReadCount(scope, m.nativePDFLife.context(), m.model, content, env)
	}
	settings, err := m.command.Startup.GetInitialSettings()
	if err != nil {
		return nil, nil
	}
	credentials, err := auth.nativeReadCountCredentials(scope, env)
	if err != nil {
		return nil, nil
	}
	credentials, err = auth.nativeReadCountCloud(scope, m.nativePDFLife.context(), m.model, env, credentials)
	if err != nil {
		return nil, nil
	}
	betas, err := nativeReadCountingBetas(m.model, env, *settings, credentials.oauth)
	if err != nil {
		return nil, nil
	}
	redacted, err := auth.nativeThinkingSummaryBeta(scope, m.model)
	if err != nil {
		return nil, nil
	}
	betas = requestcontext.WithThinkingSummaryBeta(betas, redacted)
	model, session, life := m.model, m.nativePDFSessionID, m.nativePDFLife.context()
	if m.state != nil {
		session = m.state.Snapshot().SessionID
	}
	work, err := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (*float64, error) {
		return nativeReadCountRequest(life, func() (*provider.Anthropic, error) { return auth.nativeCountClient(env, session, credentials) }, provider.AuxiliaryRetry{Now: n.Now, Random: nativeMathRandom, Sleep: retrypolicy.SleepAttempt}, model, content, betas)
	})
	if err != nil {
		return nil, err
	}
	return work.Await(scope)
}
