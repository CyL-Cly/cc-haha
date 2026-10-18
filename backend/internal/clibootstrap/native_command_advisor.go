package clibootstrap

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// This is main.tsx's actual Commander option-registration consumer, before
// preAction. Its beta predicate samples the API-key source before OAuth; MCP
// catalog eligibility is a separate later consumer and cannot stand in for it.
func (m *MainCommand) initializeNativeCommandAdvisor(ctx context.Context, scope *cooperative.Scope) error {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.Native == nil || !m.Native.NativeProcess || m.Settings == nil {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	env := m.nativeCommandEnvironment()
	m.commandAdvisorConfigurable = false
	if envTruthyNative(env["CLAUDE_CODE_DISABLE_ADVISOR_TOOL"]) {
		return nil
	}
	provider := "firstParty"
	for _, entry := range []struct{ env, value string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTruthyNative(env[entry.env]) {
			provider = entry.value
			break
		}
	}
	if provider != "firstParty" && provider != "foundry" {
		return nil
	}
	firstPartyURL := env["ANTHROPIC_BASE_URL"] == ""
	if !firstPartyURL {
		parsed, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
		firstPartyURL = err == nil && (parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com")
	}
	if provider == "firstParty" && !firstPartyURL {
		subscriber, err := m.nativeCommandAdvisorSubscriber(ctx, scope)
		if err != nil || !subscriber {
			return err
		}
	}
	// This check is deliberately after the subscriber predicate, as in betas.ts.
	if envTruthyNative(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		return nil
	}
	config := protocol.NewJSONObject()
	// The frozen external distribution has no GrowthBook client key. Its
	// genuine ant overrides precede that gate; cached disk features do not.
	if env["USER_TYPE"] == "ant" {
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON([]byte(env["CLAUDE_INTERNAL_FC_OVERRIDES"])) != nil || overrides.Get("tengu_sage_compass") == nil {
			global, err := m.Global.Get()
			if err != nil {
				return err
			}
			if global != nil {
				_ = overrides.UnmarshalJSON(global.Get("growthBookOverrides"))
			}
		}
		if raw := overrides.Get("tengu_sage_compass"); raw != nil {
			if err := config.UnmarshalJSON(raw); err != nil || !config.IsObject() {
				return mainCap("native command advisor configuration value")
			}
		}
	}
	m.commandAdvisorConfigurable = openaiauth.AuthTruthy(config.Get("enabled")) && openaiauth.AuthTruthy(config.Get("canUserConfigure"))
	return nil
}

func (m *MainCommand) nativeCommandAdvisorSubscriber(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	env := m.nativeCommandEnvironment()
	if envTruthyNative(env["CLAUDE_CODE_SIMPLE"]) {
		return false, nil
	}
	helper := []byte(nil)
	source := "none"
	if env["ANTHROPIC_UNIX_SOCKET"] == "" {
		settings, err := m.Settings.InitialSettings()
		if err != nil {
			return false, err
		}
		if settings != nil {
			helper = settings.Get("apiKeyHelper")
		}
		source, _, err = m.nativeCommandAPIKeySource(ctx, scope, helper, m.NonInteractive && env["CLAUDE_CODE_ENTRYPOINT"] != "claude-vscode")
		if err != nil {
			var capability *MainCapabilityError
			if context.Cause(ctx) != nil || errors.Is(err, cooperative.ErrRestricted) || errors.As(err, &capability) {
				return false, err
			}
			source = "none" // original isAnthropicAuthEnabled catches this lookup
		}
	}
	if !openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: false, Environment: env, APIKeySource: source, APIKeyHelper: helper}) {
		return false, nil
	}
	tokens, err := m.nativeCommandClaudeTokens(ctx, scope)
	if err != nil {
		return false, err
	}
	return openaiauth.ClaudeSubscriber(true, tokens)
}
