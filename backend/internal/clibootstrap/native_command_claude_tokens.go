package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type nativeCommandClaudeTokenMemo struct {
	store  *securestorage.Store
	tokens *openaiauth.Value
}

// getClaudeAIOAuthTokens memoizes an actual value or absence. In particular,
// the inference-only forced token never reads the credentials Store.
func (m *MainCommand) nativeCommandClaudeTokens(ctx context.Context, scope *cooperative.Scope) (*openaiauth.Value, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.Secure == nil || m.Native == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if runtime := m.nativeKeychainPrefetch; runtime != nil {
		if err := runtime.check(scope); err != nil {
			return nil, err
		}
	}
	if memo := m.commandClaudeTokens; memo != nil {
		if memo.store != m.Secure {
			return nil, cooperative.ErrRestricted
		}
		return memo.tokens, nil
	}
	var tokens *openaiauth.Value
	if !envTruthyNative(m.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		if value := m.Native.Environment["CLAUDE_CODE_OAUTH_TOKEN"]; value != "" {
			tokens = openaiauth.ClaudeEnvironmentToken(value)
		} else if value := m.credentialFromDescriptor("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", ".oauth_token"); value != "" {
			tokens = openaiauth.ClaudeEnvironmentToken(value)
		} else {
			raw, err := m.Secure.ReadSourceJSON(ctx)
			if err != nil {
				return nil, err // an interrupted operation cannot memoize absence
			}
			tokens = openaiauth.ClaudeTokensFromSecure(raw)
		}
	}
	m.commandClaudeTokens = &nativeCommandClaudeTokenMemo{store: m.Secure, tokens: tokens}
	return tokens, nil
}

func (m *MainCommand) clearNativeCommandLegacyAPIKey() {
	m.commandLegacyAPIKey = nil
	if runtime := m.nativeKeychainPrefetch; runtime != nil && runtime.ticket.Owns(m.Secure) {
		runtime.ticket.ClearLegacy()
	}
}
