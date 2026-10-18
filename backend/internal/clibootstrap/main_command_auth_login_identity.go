package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// cliAuthIdentity shares the ordinary command's existing auth-source producer.
// Status and post-login policy checks consume the same owner and credential stores.
func (m *MainCommand) cliAuthIdentity(ctx context.Context, scope *cooperative.Scope, preferThirdParty bool) (cliauth.GeneralStatusInput, *openaiauth.Value, bool, bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return cliauth.GeneralStatusInput{}, nil, false, false, cooperative.ErrRestricted
	}
	env := m.Native.Environment
	settings, err := m.Settings.InitialSettings()
	if err != nil {
		return cliauth.GeneralStatusInput{}, nil, false, false, err
	}
	if settings == nil {
		empty := protocol.NewJSONObject()
		settings = &empty
	}
	global, err := m.Global.Get()
	if err != nil {
		return cliauth.GeneralStatusInput{}, nil, false, false, err
	}
	if global == nil {
		empty := protocol.NewJSONObject()
		global = &empty
	}
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	managed := envTruthyNative(env["CLAUDE_CODE_REMOTE"]) || env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	homespace := env["USER_TYPE"] == "ant" && envTruthyNative(env["COO_RUNNING_ON_HOMESPACE"])
	readOAuthFD := func() string {
		return m.credentialFromDescriptor("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", ".oauth_token")
	}
	helper := settings.Get("apiKeyHelper")
	if bare {
		flag, e := m.Startup.GetSettingsForSource("flagSettings")
		if e != nil {
			return cliauth.GeneralStatusInput{}, nil, false, false, e
		}
		helper = nil
		if flag != nil {
			helper = flag.Get("apiKeyHelper")
		}
	}
	provider := "firstParty"
	for _, entry := range []struct{ key, value string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTruthyNative(env[entry.key]) {
			provider = entry.value
			break
		}
	}
	input := cliauth.GeneralStatusInput{AuthTokenSource: "none", APIKeySource: "none", APIProvider: provider, HasAPIKeyEnvironment: env["ANTHROPIC_API_KEY"] != "" && !homespace, UsingThirdParty: provider != "firstParty"}
	if bare && openaiauth.AuthTruthy(helper) {
		input.AuthTokenSource = "apiKeyHelper"
	} else if !bare {
		if env["ANTHROPIC_AUTH_TOKEN"] != "" && !managed {
			input.AuthTokenSource = "ANTHROPIC_AUTH_TOKEN"
		} else if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			input.AuthTokenSource = "CLAUDE_CODE_OAUTH_TOKEN"
		} else if readOAuthFD() != "" {
			input.AuthTokenSource = "CCR_OAUTH_TOKEN_FILE"
			if env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] != "" {
				input.AuthTokenSource = "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"
			}
		} else if openaiauth.AuthTruthy(helper) && !managed {
			input.AuthTokenSource = "apiKeyHelper"
		}
	}
	var tokens *openaiauth.Value
	if !bare {
		tokens, err = m.nativeCommandClaudeTokens(ctx, scope)
		if err != nil {
			return cliauth.GeneralStatusInput{}, nil, false, false, err
		}
		if input.AuthTokenSource == "none" {
			subscriber, e := openaiauth.ClaudeSubscriber(true, tokens)
			if e != nil {
				return cliauth.GeneralStatusInput{}, nil, false, false, e
			}
			if subscriber && tokens != nil && openaiauth.AuthTruthy(tokens.Get("accessToken")) {
				input.AuthTokenSource = "claude.ai"
			}
		}
	}
	input.HasToken = input.AuthTokenSource != "none"
	source, keyPresent, err := m.nativeCommandAPIKeySource(ctx, scope, helper, preferThirdParty)
	if err != nil {
		return cliauth.GeneralStatusInput{}, nil, false, false, err
	}
	input.APIKeySource = source
	enabled := openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: bare, Environment: env, APIKeySource: input.APIKeySource, APIKeyHelper: helper})
	if enabled {
		input.OAuthAccount = global.Get("oauthAccount")
		if tokens != nil {
			input.SubscriptionType = tokens.Get("subscriptionType")
		}
	}
	return input, tokens, keyPresent, enabled, nil
}
