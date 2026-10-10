package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func IsGrokClientEnvironment(name string) bool {
	return name == "CC_HAHA_GROK_OAUTH_PROVIDER" || name == "GROK_OAUTH_FILE"
}
func selectGrok(ctx context.Context, input Input) (*provider.GrokOAuthConfig, error) {
	if !openAIEnvTruthy(input.Context.Host.Env["CC_HAHA_GROK_OAUTH_PROVIDER"]) {
		return nil, nil
	}
	binding := input.Context.Host.NativeGrokOAuth
	if binding == nil || binding.HasAuth == nil || binding.FreshTokens == nil {
		return nil, unsupported("actual Grok credential consumer")
	}
	active, err := binding.HasAuth(ctx)
	if err != nil {
		return nil, err
	}
	if active {
		return binding, nil
	}
	return nil, nil
}
