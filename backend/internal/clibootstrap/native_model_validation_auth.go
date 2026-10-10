package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// validateModel's sideQuery shares the ordinary CLI credential/cache producer.
// This binding belongs only to its sealed Host API; generic ASK and the
// existing desktop-managed environment callback retain their own admission.
func (m *MainPrint) nativeModelValidationClaudeAuth(scope *cooperative.Scope) (*requestretry.NativeClaudeAuth, error) {
	if !scope.BelongsTo(m.Owner) || m.openAIAuth == nil {
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
		tokens, err := m.openAIAuth.claudeTokensFor(owned, env, pickerTrue(env["CLAUDE_CODE_SIMPLE"]))
		if err != nil || tokens == nil {
			return nil, err
		}
		value := tokens.Object.String("accessToken")
		return &value, nil
	})
}
