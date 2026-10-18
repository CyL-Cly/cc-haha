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
	// getAnthropicClient uses the cached CLI subscriber identity for ordinary
	// requests as well as desktop requests. The live desktop token-update API
	// remains restricted to its managed entrypoint; both use this owned reader.
	if m.options.Native == nil || !m.options.Native.NativeProcess {
		if m.nativeOpenAIEnvironment()["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop" {
			// Selecting the desktop auth domain cannot mint its producer on a
			// finite/offline graph. Ordinary native CLI uses the same real reader.
			return nil, cooperative.ErrRestricted
		}
		return nil, nil
	}
	// An unselected optional auth domain has no producer on the offline print
	// graph. Its absence is not an owner/continuation failure. Once selected,
	// the desktop domain still requires the real native credential consumer.
	if m.openAIAuth == nil {
		return nil, cooperative.ErrRestricted
	}
	return requestretry.NewNativeClaudeAuth(m.Owner, scope, func(ctx context.Context) (bool, error) {
		// getAnthropicClient resolves cached subscriber/auth state before
		// messages.create receives the model AbortSignal. A queued submit that
		// already aborted this turn cannot cancel those signal-free getters.
		return m.openAIAuth.IsClaudeSubscriber(context.WithoutCancel(ctx), scope, !m.options.Interactive)
	}, func(ctx context.Context) (*string, error) {
		owned, err := m.openAIAuth.owned(context.WithoutCancel(ctx), scope)
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
	}, func(ctx context.Context) error {
		return m.openAIAuth.checkClaudeTokensBeforeClient(context.WithoutCancel(ctx), scope)
	})
}
