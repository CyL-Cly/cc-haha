package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// initialize is an observation of the same catalog, settings and authentication
// producers as the ordinary print session. It neither starts ASK nor captures a
// scope for later SDK callbacks.
func (m *MainPrint) nativeSDKInitializeMetadata(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.openAIAuth == nil || m.agents == nil || m.styles == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	env, err := m.pickerEnvironment()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	identity, err := m.openAIAuth.AnthropicModelIdentity(context.Background(), scope, true)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	selector := m.nativeModelSelector
	if selector == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	global, err := m.Global.Get()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	rawInitial, err := selector.UserSpecified()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var initial *string
	if len(rawInitial) > 0 && string(rawInitial) != "null" {
		value, e := decodeNativeModel(rawInitial)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		initial = pickerPointer(value)
	}
	openAIValue, e := m.openAIAuth.GetCached(context.Background(), scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	openAIActive, e := m.openAIAuth.ModelActive(context.Background(), scope, true)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	options, err := nativeSDKModelOptions(env, merged, global, selector, initial, identity, openaiauth.HasOpenAIAuth(openAIValue), openAIActive)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	models := []protocol.JSONObject{}
	for _, option := range options {
		modelID := "default"
		if option.Value != nil {
			modelID = *option.Value
		}
		resolved, e := nativePickerResolve(selector, option.Value)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		info := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: quoteMain(modelID)}, protocol.JSONField{Name: "displayName", Value: quoteMain(option.Label)}, protocol.JSONField{Name: "description", Value: quoteMain(option.Description)}, protocol.JSONField{Name: "supportsAutoMode", Value: []byte("true")})
		support, levels, adaptive := requestcontext.NativeSDKModelCapabilities(resolved, env, identity.Subscriber)
		if support {
			info.Set("supportsEffort", []byte("true"))
			b, _ := json.Marshal(levels)
			info.Set("supportedEffortLevels", b)
		}
		if adaptive {
			info.Set("supportsAdaptiveThinking", []byte("true"))
		}
		if !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) && strings.Contains(strings.ToLower(resolved), "opus-4-6") {
			info.Set("supportsFastMode", []byte("true"))
		}
		models = append(models, info)
	}
	definitions, err := m.agents.List(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	agents := []protocol.JSONObject{}
	for _, d := range definitions.Active {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(d.Name)}, protocol.JSONField{Name: "description", Value: quoteMain(d.Description)})
		if d.Model != "" && d.Model != "inherit" {
			row.Set("model", quoteMain(d.Model))
		}
		agents = append(agents, row)
	}
	styles, err := m.styles.List(context.Background(), m.workingDirectory())
	if err != nil {
		return protocol.JSONObject{}, err
	}
	names := []string{}
	for _, style := range styles {
		names = append(names, style.Name)
	}
	account, err := m.nativeSDKAccount(scope, env, identity)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	outputStyle := merged.Get("outputStyle")
	if !openaiauth.AuthTruthy(outputStyle) {
		outputStyle = quoteMain("default")
	}
	encode := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	return protocol.NewJSONObject(protocol.JSONField{Name: "commands", Value: encode(m.Registry.SDKInitializeCommands())}, protocol.JSONField{Name: "agents", Value: encode(agents)}, protocol.JSONField{Name: "output_style", Value: outputStyle}, protocol.JSONField{Name: "available_output_styles", Value: encode(names)}, protocol.JSONField{Name: "models", Value: encode(models)}, protocol.JSONField{Name: "account", Value: encode(account)}, protocol.JSONField{Name: "pid", Value: encode(m.options.PID)}), nil
}
func (m *MainPrint) nativeSDKHookError(scope *cooperative.Scope, id string, failure error) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	name := "Error"
	var typed *sdkhost.NativeSDKCallbackError
	if errors.As(failure, &typed) {
		name = typed.Name
	}
	text := name
	if failure.Error() != "" {
		text += ": " + failure.Error()
	}
	_, err := fmt.Fprintf(m.options.Error, "Error in hook callback %s: %s\n", id, text)
	return err
}
func (m *MainPrint) nativeSDKAccount(scope *cooperative.Scope, env map[string]string, identity modelselection.MetadataIdentity) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject(protocol.JSONField{Name: "apiProvider", Value: quoteMain("firstParty")})
	for _, item := range []struct{ key, provider string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if pickerTrue(env[item.key]) {
			result.Set("apiProvider", quoteMain(item.provider))
			return result, nil
		}
	}
	b := m.openAIAuth
	ctx, e := b.owned(context.Background(), scope)
	if e != nil {
		return result, e
	}
	bare := pickerTrue(env["CLAUDE_CODE_SIMPLE"])
	settings, e := b.authDependencies(env, bare)
	if e != nil {
		return result, e
	}
	source, e := b.apiKeySource(ctx, env, bare, true)
	if e != nil {
		return result, e
	}
	tokenSource := "none"
	managed := env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	var tokens *openaiauth.Value
	if !bare {
		if env["ANTHROPIC_AUTH_TOKEN"] != "" && !managed {
			tokenSource = "ANTHROPIC_AUTH_TOKEN"
		} else if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			tokenSource = "CLAUDE_CODE_OAUTH_TOKEN"
		} else if b.credentialFromDescriptor(env, "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", ".oauth_token") != "" {
			tokenSource = "CCR_OAUTH_TOKEN_FILE"
			if env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] != "" {
				tokenSource = "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"
			}
		} else if openaiauth.AuthTruthy(settings.Get("apiKeyHelper")) && !managed {
			tokenSource = "apiKeyHelper"
		} else {
			tokens, e = b.claudeTokensFor(ctx, env, bare)
			if e != nil {
				return result, e
			}
			subscriber, e := openaiauth.ClaudeSubscriber(true, tokens)
			if e != nil {
				return result, e
			}
			if subscriber {
				tokenSource = "claude.ai"
			}
		}
	} else if openaiauth.AuthTruthy(settings.Get("apiKeyHelper")) {
		tokenSource = "apiKeyHelper"
	}
	openAI, e := b.GetCached(context.Background(), scope)
	if e != nil {
		return result, e
	}
	if tokenSource == "none" && source == "none" && openaiauth.HasOpenAIAuth(openAI) {
		result.Set("subscriptionType", quoteMain("ChatGPT Pro/Plus"))
		result.Set("tokenSource", quoteMain("OpenAI OAuth"))
		if openAI.Get("email") != nil {
			result.Set("email", openAI.Get("email"))
		}
		return result, nil
	}
	if tokenSource == "CLAUDE_CODE_OAUTH_TOKEN" || tokenSource == "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR" || !identity.Subscriber {
		result.Set("tokenSource", quoteMain(tokenSource))
	} else {
		name := "Claude API"
		switch identity.SubscriptionType {
		case "max":
			name = "Claude Max"
		case "pro":
			name = "Claude Pro"
		case "team":
			name = "Claude Team"
		case "enterprise":
			name = "Claude Enterprise"
		}
		result.Set("subscriptionType", quoteMain(name))
	}
	if source != "none" {
		result.Set("apiKeySource", quoteMain(source))
	}
	if tokenSource == "claude.ai" || source == "/login managed key" {
		global, e := m.Global.Get()
		if e != nil {
			return result, e
		}
		if global != nil {
			account, _ := global.Object("oauthAccount")
			for _, item := range []struct{ from, to string }{{"emailAddress", "email"}, {"organizationName", "organization"}} {
				if openaiauth.AuthTruthy(account.Get(item.from)) {
					result.Set(item.to, account.Get(item.from))
				}
			}
		}
	}
	return result, nil
}
