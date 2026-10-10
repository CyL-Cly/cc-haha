package clibootstrap

import (
	"context"
	"errors"
	"io/fs"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (b *NativeOpenAIAuth) authDependencies(env map[string]string, bare bool) (protocol.JSONObject, error) {
	if envTruthyNative(env["CLAUDE_CODE_REMOTE"]) || env["USER_TYPE"] == "ant" && envTruthyNative(env["COO_RUNNING_ON_HOMESPACE"]) {
		return protocol.JSONObject{}, mainCap("native account remote/homespace credential producers")
	}
	for _, key := range []string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"} {
		if !bare && env[key] != "" {
			return protocol.JSONObject{}, mainCap("native account file descriptor credential producer")
		}
	}
	var settings *protocol.JSONObject
	var err error
	if bare {
		settings, err = b.main.Startup.GetSettingsForSource("flagSettings")
	} else {
		settings, err = b.main.Startup.GetInitialSettings()
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if settings == nil {
		empty := protocol.NewJSONObject()
		settings = &empty
	}
	helper := settings.Get("apiKeyHelper")
	if len(helper) > 0 && string(helper) != "null" && string(helper) != `""` && b.main.apiKeyHelper == nil {
		return protocol.JSONObject{}, mainCap("native account apiKeyHelper credential producer")
	}
	return *settings, nil
}
func (b *NativeOpenAIAuth) checkRemoteFallback() error {
	for _, path := range []string{"/home/claude/.claude/remote/.oauth_token", "/home/claude/.claude/remote/.api_key"} {
		if _, err := b.main.options.Native.stat(path); err == nil {
			return mainCap("native account remote well-known credential file")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
func (b *NativeOpenAIAuth) claudeTokensFor(ctx context.Context, env map[string]string, bare bool) (*openaiauth.Value, error) {
	if b.claudeTokensLoaded {
		return b.claudeTokens, nil
	}
	if !bare {
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			b.claudeTokens = openaiauth.ClaudeEnvironmentToken(env["CLAUDE_CODE_OAUTH_TOKEN"])
		} else {
			if err := b.checkRemoteFallback(); err != nil {
				return nil, err
			}
			raw, err := b.main.secure.ReadSourceJSON(ctx)
			if err != nil {
				return nil, err
			}
			b.claudeTokens = openaiauth.ClaudeTokensFromSecure(raw)
		}
	}
	b.claudeTokensLoaded = true
	return b.claudeTokens, nil
}
func (b *NativeOpenAIAuth) apiKeySource(ctx context.Context, env map[string]string, bare, preferThirdParty bool) (string, error) {
	if env["ANTHROPIC_API_KEY"] != "" && (bare || preferThirdParty || envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test") {
		return "ANTHROPIC_API_KEY", nil
	}
	if bare {
		command, err := b.main.nativeConfiguredAPIKeyHelper()
		if err != nil {
			return "", err
		}
		if command != "" {
			return "apiKeyHelper", nil
		}
		return "none", nil
	}
	if envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test" {
		if env["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
			return "", errors.New("ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN env var is required")
		}
		return "none", nil
	}
	if env["ANTHROPIC_API_KEY"] != "" {
		global, err := b.main.Global.Get()
		if err != nil {
			return "", err
		}
		if global != nil {
			approved, err := openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(env["ANTHROPIC_API_KEY"]))
			if err != nil {
				return "", err
			}
			if approved {
				return "ANTHROPIC_API_KEY", nil
			}
		}
	}
	if err := b.checkRemoteFallback(); err != nil {
		return "", err
	}
	command, err := b.main.nativeConfiguredAPIKeyHelper()
	if err != nil {
		return "", err
	}
	if command != "" {
		return "apiKeyHelper", nil
	}
	if !b.legacyAPIKeyLoaded {
		key, err := b.main.secure.ReadLegacyManagedAPIKey(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			if reportError := b.main.nativeLogError(ctx, err); reportError != nil {
				return "", reportError
			}
		}
		if key == "" {
			global, err := b.main.Global.Get()
			if err != nil {
				return "", err
			}
			if global != nil {
				key = global.String("primaryApiKey")
				b.legacyAPIKeyPresent = openaiauth.AuthTruthy(global.Get("primaryApiKey"))
			}
		}
		b.legacyAPIKeyPresent = b.legacyAPIKeyPresent || key != ""
		b.legacyAPIKey, b.legacyAPIKeyLoaded = key, true
	}
	if b.legacyAPIKeyPresent {
		return "/login managed key", nil
	}
	return "none", nil
}

// IsClaudeSubscriber receives the real request's noninteractive state. The
// unported VSCode bootstrap clientType is guarded, not inferred as a CLI value.
func (b *NativeOpenAIAuth) IsClaudeSubscriber(ctx context.Context, scope *cooperative.Scope, nonInteractive bool) (bool, error) {
	identity, err := b.AnthropicModelIdentity(ctx, scope, nonInteractive)
	return identity.Subscriber, err
}

// AnthropicModelIdentity reads the same cached CLI credential producer used
// by isClaudeAISubscriber/getSubscriptionType/getRateLimitTier. Disabled auth
// must not expose a competing account's subscription metadata.
func (b *NativeOpenAIAuth) AnthropicModelIdentity(ctx context.Context, scope *cooperative.Scope, nonInteractive bool) (modelselection.MetadataIdentity, error) {
	identity := modelselection.MetadataIdentity{}
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return identity, err
	}
	env := b.main.nativeOpenAIEnvironment()
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	if bare {
		return identity, nil
	}
	if env["CLAUDE_CODE_ENTRYPOINT"] == "claude-vscode" {
		return identity, mainCap("actual VSCode bootstrap clientType auth preference")
	}
	settings, err := b.authDependencies(env, bare)
	if err != nil {
		return identity, err
	}
	source, err := b.apiKeySource(ctx, env, bare, nonInteractive)
	if err != nil {
		var capability *MainCapabilityError
		if errors.As(err, &capability) || errors.Is(err, cooperative.ErrRestricted) || ctx.Err() != nil {
			return identity, err
		}
		source = "none"
	}
	enabled := openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: bare, Environment: env, APIKeySource: source, APIKeyHelper: settings.Get("apiKeyHelper")})
	if !enabled {
		return identity, nil
	}
	tokens, err := b.claudeTokensFor(ctx, env, bare)
	if err != nil {
		return identity, err
	}
	identity.Subscriber, err = openaiauth.ClaudeSubscriber(true, tokens)
	if tokens != nil {
		identity.SubscriptionType = tokens.Object.String("subscriptionType")
		identity.RateLimitTier = tokens.Object.String("rateLimitTier")
	}
	return identity, err
}

// ModelActive ports isOpenAIAuthActive's real saved-token and competing-auth
// consumers. It never treats an OAuth-file environment variable as an account.
func (b *NativeOpenAIAuth) ModelActive(ctx context.Context, scope *cooperative.Scope, nonInteractive bool) (bool, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return false, err
	}
	value, err := b.GetCached(ctx, scope)
	if err != nil || !openaiauth.HasOpenAIAuth(value) {
		return false, err
	}
	env := b.main.nativeOpenAIEnvironment()
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthyNative(env[key]) {
			return false, nil
		}
	}
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	if env["CLAUDE_CODE_ENTRYPOINT"] == "claude-vscode" {
		return false, mainCap("actual VSCode bootstrap clientType auth preference")
	}
	if _, err = b.authDependencies(env, bare); err != nil {
		return false, err
	}
	authSource := "none"
	if !bare {
		managed := env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
		if env["ANTHROPIC_AUTH_TOKEN"] != "" && !managed {
			authSource = "ANTHROPIC_AUTH_TOKEN"
		} else if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			authSource = "CLAUDE_CODE_OAUTH_TOKEN"
		} else {
			tokens, err := b.claudeTokensFor(ctx, env, bare)
			if err != nil {
				return false, err
			}
			claude, err := openaiauth.ClaudeSubscriber(true, tokens)
			if err != nil {
				return false, err
			}
			if claude {
				authSource = "claude.ai"
			}
		}
	}
	source, err := b.apiKeySource(ctx, env, bare, nonInteractive)
	if err != nil {
		return false, err
	}
	return source == "none" && authSource == "none", nil
}
func (b *NativeOpenAIAuth) ClearClaudeTokenCache(scope *cooperative.Scope) error {
	if b == nil || !scope.BelongsTo(b.main.Owner) {
		return cooperative.ErrRestricted
	}
	b.claudeTokens = nil
	b.claudeTokensLoaded = false
	b.main.secure.Clear()
	return nil
}
