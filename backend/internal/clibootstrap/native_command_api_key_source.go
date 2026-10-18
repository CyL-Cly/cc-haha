package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
)

// The API-key source is sampled before OAuth by isAnthropicAuthEnabled.
// skipRetrievingKeyFromApiKeyHelper selects its source without running a helper.
// Auth status reuses this same producer at its existing later sampling point.
func (m *MainCommand) nativeCommandAPIKeySource(ctx context.Context, scope *cooperative.Scope, helper json.RawMessage, preferThirdParty bool) (string, bool, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.Native == nil || m.Global == nil || m.Secure == nil {
		return "", false, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return "", false, err
	}
	if err := context.Cause(ctx); err != nil {
		return "", false, err
	}
	env := m.Native.Environment
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	key := env["ANTHROPIC_API_KEY"]
	if !bare && env["USER_TYPE"] == "ant" && envTruthyNative(env["COO_RUNNING_ON_HOMESPACE"]) {
		key = ""
	}
	if key != "" && (bare || preferThirdParty) {
		return "ANTHROPIC_API_KEY", true, nil
	}
	if bare {
		if openaiauth.AuthTruthy(helper) {
			return "apiKeyHelper", false, nil
		}
		return "none", false, nil
	}
	readFD := func() string { return m.credentialFromDescriptor("CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key") }
	if envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test" {
		if readFD() != "" || key != "" {
			return "ANTHROPIC_API_KEY", true, nil
		}
		if env["CLAUDE_CODE_OAUTH_TOKEN"] == "" && env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] == "" {
			return "", false, errors.New("ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN env var is required")
		}
		return "none", false, nil
	}
	approved := false
	if key != "" {
		global, err := m.Global.Get()
		if err != nil {
			return "", false, err
		}
		if global != nil {
			approved, err = openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(key))
			if err != nil {
				return "", false, err
			}
		}
	}
	if approved || readFD() != "" {
		return "ANTHROPIC_API_KEY", true, nil
	}
	if openaiauth.AuthTruthy(helper) {
		return "apiKeyHelper", false, nil
	}
	_, present, err := m.nativeCommandLegacyAPIKey(ctx, scope)
	if err != nil {
		return "", false, err
	}
	if present {
		return "/login managed key", true, nil
	}
	return "none", false, nil
}
