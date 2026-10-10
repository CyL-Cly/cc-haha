package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// SetDesktopOAuthToken is the native consumer of conversationService's
// update_environment_variables before-turn message. It updates the existing
// owner and credential cache; it cannot turn a CLI/custom-provider session into
// a desktop-managed Claude session.
func (m *MainPrint) SetDesktopOAuthToken(ctx context.Context, token string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || !m.initialized || m.Host == nil {
			return cooperative.ErrClosed
		}
		if m.nativeOpenAIEnvironment()["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" || token == "" {
			return cooperative.ErrRestricted
		}
		if err := m.environment.SetProperty(quoteMain("CLAUDE_CODE_OAUTH_TOKEN"), quoteMain(token)); err != nil {
			return err
		}
		if err := m.openAIAuth.ClearClaudeTokenCache(scope); err != nil {
			return err
		}
		return m.Host.SetNativeRequestEnvironment(scope, m.nativeOpenAIEnvironment())
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func (m *MainPrint) nativeClaudeAuthBinding(scope *cooperative.Scope) (*requestretry.NativeClaudeAuth, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	// Existing standalone CLI credential consumers are not changed by the
	// desktop bridge. Only the real managed-OAuth entrypoint installs this branch.
	if m.nativeOpenAIEnvironment()["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" {
		return nil, nil
	}
	// An unselected optional auth domain has no producer on the offline print
	// graph. Its absence is not an owner/continuation failure. Once selected,
	// the desktop domain still requires the real native credential consumer.
	if m.openAIAuth == nil {
		return nil, cooperative.ErrRestricted
	}
	return requestretry.NewNativeClaudeAuth(m.Owner, scope, func(ctx context.Context) (bool, error) {
		return m.openAIAuth.IsClaudeSubscriber(ctx, scope, !m.options.Interactive)
	}, func(ctx context.Context) (*string, error) {
		owned, err := m.openAIAuth.owned(ctx, scope)
		if err != nil {
			return nil, err
		}
		env := m.nativeOpenAIEnvironment()
		tokens, err := m.openAIAuth.claudeTokensFor(owned, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
		if err != nil || tokens == nil {
			return nil, err
		}
		var token string
		if json.Unmarshal(tokens.Get("accessToken"), &token) != nil {
			return nil, mainCap("Claude OAuth access token string")
		}
		return &token, nil
	})
}
