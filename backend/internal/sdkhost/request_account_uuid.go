package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) nativeRequestAccountMetadata(scope *cooperative.Scope) func() (string, error) {
	if h.options.Print == nil || !h.options.Print.NativeProcess {
		return nil
	}
	return func() (string, error) {
		if scope == nil || !scope.BelongsTo(h.options.Owner) {
			return "", cooperative.ErrRestricted
		}
		source := ""
		if h.options.NativeAPIKeySource != nil {
			var err error
			source, err = h.options.NativeAPIKeySource(scope)
			if err != nil {
				return "", err
			}
		}
		settings, err := h.nativeSDKInitialSettings()
		if err != nil {
			return "", err
		}
		enabled := openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: costAuthEnvTruthy(h.options.OriginalEnv["CLAUDE_CODE_SIMPLE"]), Environment: h.options.OriginalEnv, APIKeySource: source, APIKeyHelper: settings.Get("apiKeyHelper")})
		if !enabled {
			return "", nil
		}
		if account := protocol.TrimJavaScriptString(h.options.OriginalEnv["CLAUDE_CODE_ACCOUNT_UUID"]); account != "" {
			return account, nil
		}
		global, err := h.options.Global.Get()
		if err != nil {
			return "", err
		}
		var account protocol.JSONObject
		_ = account.UnmarshalJSON(global.Get("oauthAccount"))
		return account.String("accountUuid"), nil
	}
}
