package requestretry

import (
	"context"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func IsOpenAIClientEnvironment(name string) bool {
	switch name {
	case "CC_HAHA_OPENAI_OAUTH_PROVIDER", "OPENAI_CODEX_OAUTH_FILE", "OPENAI_CODEX_MODEL", "OPENAI_CODEX_HAIKU_MODEL", "OPENAI_CODEX_SONNET_MODEL", "OPENAI_CODEX_OPUS_MODEL", "CC_HAHA_OPENAI_REASONING_EFFORT", "CC_HAHA_OPENAI_REQUEST_COMPRESSION":
		return true
	}
	return false
}

// Selection runs in the actual GetClient callback, after once-per-query
// preparation. The installed storage consumer has not read credentials during
// request construction or profile admission.
func selectOpenAICodex(ctx context.Context, input Input) (*provider.OpenAICodexConfig, error) {
	config := input.Context.Host.NativeCodexOAuth
	if config == nil {
		return nil, nil
	}
	if config.HasAuth == nil || config.FreshTokens == nil {
		return nil, unsupported("actual OpenAI credential consumer")
	}
	hasAuth, err := config.HasAuth(ctx)
	if err != nil {
		return nil, err
	}
	env := input.Context.Host.Env
	subscriber := false
	if config.IsClaudeSubscriber != nil && !openAIEnvTruthy(env["CC_HAHA_GROK_OAUTH_PROVIDER"]) {
		subscriber, err = config.IsClaudeSubscriber(ctx, input.Context.Options.IsNonInteractiveSession)
		if err != nil {
			return nil, err
		}
	}
	selected := openaiauth.ShouldUseCodexTransport(openaiauth.CodexSelection{HasOpenAIAuth: hasAuth, IsClaudeSubscriber: subscriber, ForceOpenAICodex: openAIEnvTruthy(env["CC_HAHA_OPENAI_OAUTH_PROVIDER"]), IsOpenAIModel: providerproxy.IsOpenAIResponsesModel(input.Context.Options.Model), HasAnthropicAuthToken: env["ANTHROPIC_AUTH_TOKEN"] != "", HasExplicitAPIKey: false, HasFallbackAPIKey: hasAuth && env["ANTHROPIC_AUTH_TOKEN"] == "" && env["ANTHROPIC_API_KEY"] != ""})
	if selected {
		return config, nil
	}
	return nil, nil
}
func openAIEnvTruthy(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, func(r rune) bool {
		return r == 0xfeff || r == 0x2028 || r == 0x2029 || r == ' ' || r >= 9 && r <= 13 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x202f || r == 0x205f || r == 0x3000
	})) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
