package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// Borrow the existing credential/helper/cloud methods on the command's actual
// owner and stores. This is an auth producer bundle, not NewMainPrint: no ASK,
// Controller, transcript, tool registry or model loop is allocated here.
func (m *NativeMCPToolContext) ordinaryModelAuth(tls *tlsconfig.State) (*MainPrint, error) {
	c := m.command
	auth := &MainPrint{Owner: c.Owner, Global: c.Global, Startup: c.Startup, state: m.state, environment: c.Settings.environment, secure: c.Secure, tls: tls, commandBinding: m.binding, options: MainPrintOptions{Native: c.Native, Version: c.version, Platform: m.platform, PID: c.pid, Error: c.Error}}
	auth.initializeNativeAPIKeyHelper()
	auth.initializeNativeCloudSettings()
	var err error
	auth.openAIAuth, err = auth.NewNativeOpenAIAuth(auth.nativeOpenAIRefresh)
	return auth, err
}

// Ordinary MCP uses the same cached CLI subscriber identity/token producer.
// This binding is installed only on its actual Web host or Workflow child;
// the standalone desktop/CLI admission rules remain with their existing host.
func (m *NativeMCPToolContext) ordinaryClaudeAuthBinding(scope *cooperative.Scope) (*requestretry.NativeClaudeAuth, error) {
	if e := m.checkOrdinary(scope); e != nil {
		return nil, e
	}
	auth := m.modelAuth
	if auth == nil || auth.openAIAuth == nil || !auth.options.Native.NativeProcess {
		return nil, cooperative.ErrRestricted
	}
	return requestretry.NewNativeClaudeAuth(m.command.Owner, scope, func(ctx context.Context) (bool, error) { return auth.openAIAuth.IsClaudeSubscriber(ctx, scope, true) }, func(ctx context.Context) (*string, error) {
		owned, e := auth.openAIAuth.owned(ctx, scope)
		if e != nil {
			return nil, e
		}
		env := auth.nativeOpenAIEnvironment()
		tokens, e := auth.openAIAuth.claudeTokensFor(owned, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
		if e != nil || tokens == nil {
			return nil, e
		}
		var token string
		if json.Unmarshal(tokens.Get("accessToken"), &token) != nil {
			return nil, mainCap("Claude OAuth access token string")
		}
		return &token, nil
	})
}
