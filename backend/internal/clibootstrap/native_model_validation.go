package clibootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func (m *MainPrint) validateNativeModelCommand(ctx context.Context, scope *cooperative.Scope, model string, selector *modelselection.Selector, env map[string]string, settings protocol.JSONObject) (string, error) {
	identity, err := m.openAIAuth.AnthropicModelIdentity(ctx, scope, false)
	if err != nil {
		return "", err
	}
	global, err := m.Global.Get()
	if err != nil {
		return "", err
	}
	extra := global != nil && (string(global.Get("cachedExtraUsageDisabledReason")) == "null" || global.String("cachedExtraUsageDisabledReason") == "out_of_credits")
	oneM := !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (!identity.Subscriber || extra)
	merged := !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && identity.SubscriptionType != "pro" && (!identity.Subscriber || identity.SubscriptionType != "") && !pickerTrue(env["CLAUDE_CODE_USE_BEDROCK"]) && !pickerTrue(env["CLAUDE_CODE_USE_VERTEX"]) && !pickerTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) && !pickerTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"])
	lower := strings.ToLower(model)
	if !oneM && !merged && strings.Contains(lower, "opus") && strings.Contains(lower, "[1m]") {
		return "Opus with 1M context is not available for your account. Learn more: https://code.claude.com/docs/en/model-config#extended-context-with-1m", nil
	}
	sonnet, err := nativePickerResolve(selector, pickerPointer("sonnet"))
	if err != nil {
		return "", err
	}
	if !strings.Contains(lower, "sonnet-5") && !(strings.Contains(lower, "sonnet[1m]") && strings.Contains(strings.ToLower(sonnet), "sonnet-5")) && !oneM && (strings.Contains(lower, "sonnet[1m]") || strings.Contains(lower, "sonnet-4-6[1m]")) {
		return "Sonnet with 1M context is not available for your account. Learn more: https://code.claude.com/docs/en/model-config#extended-context-with-1m", nil
	}
	for _, alias := range []string{"fable", "sonnet", "opus", "haiku", "best", "fable[1m]", "sonnet[1m]", "opus[1m]", "opusplan"} {
		if strings.ToLower(strings.TrimFunc(model, modelCommandWhitespace)) == alias {
			return "", nil
		}
	}
	if pickerTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"]) {
		if env["AZURE_OPENAI_BASE_URL"] == "" && env["AZURE_OPENAI_ENDPOINT"] == "" {
			return "AZURE_OPENAI_BASE_URL is required when using Azure OpenAI provider", nil
		}
		if env["AZURE_OPENAI_API_KEY"] == "" && env["CLAUDE_CODE_SKIP_AZURE_OPENAI_AUTH"] == "" {
			return "AZURE_OPENAI_API_KEY is required when using Azure OpenAI provider", nil
		}
		for _, name := range []string{"gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4-codex"} {
			if lower == name && env["AZURE_OPENAI_CODEX_DEPLOYMENT"] == "" && selector.SDKCatalogModelID(name) == name {
				return "Missing Azure OpenAI deployment mapping for " + name + ". Set AZURE_OPENAI_CODEX_DEPLOYMENT or settings.modelOverrides[\"" + name + "\"] to your deployment name.", nil
			}
		}
		return "", nil
	}
	if model == env["ANTHROPIC_CUSTOM_MODEL_OPTION"] && model != "" {
		return "", nil
	}
	if m.modelPicker.validModels == nil {
		m.modelPicker.validModels = map[string]bool{}
	}
	if m.modelPicker.validModels[model] {
		return "", nil
	}
	active, err := m.openAIAuth.ModelActive(ctx, scope, false)
	if err != nil {
		return "", err
	}
	if active && providerproxy.IsOpenAIResponsesModel(model) {
		m.modelPicker.validModels[model] = true
		return "", nil
	}
	err = m.Host.ValidateNativeModel(ctx, scope, model, settings)
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	if err == nil {
		m.modelPicker.validModels[model] = true
		return "", nil
	}
	var sdk *provider.SDKStreamError
	if errors.As(err, &sdk) {
		switch sdk.Class {
		case "NotFoundError":
			notice := "Model '" + model + "' not found"
			if pickerTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || pickerTrue(env["CLAUDE_CODE_USE_VERTEX"]) || pickerTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) {
				if fallback := nativeValidationFallback(model, selector); fallback != "" {
					notice += ". Try '" + fallback + "' instead"
				}
			}
			return notice, nil
		case "AuthenticationError":
			return "Authentication failed. Please check your API credentials.", nil
		case "APIConnectionError", "APIConnectionTimeoutError":
			return "Network error. Please check your internet connection.", nil
		case "APIError", "BadRequestError", "PermissionDeniedError", "ConflictError", "UnprocessableEntityError", "RateLimitError", "InternalServerError", "APIUserAbortError":
			var body protocol.JSONObject
			_ = body.UnmarshalJSON(sdk.Payload)
			if body.String("type") == "not_found_error" && strings.Contains(body.String("message"), "model:") {
				return "Model '" + model + "' not found", nil
			}
			return "API error: " + sdk.Message, nil
		}
	}
	return "Unable to validate model: " + err.Error(), nil
}

func nativeValidationFallback(model string, selector *modelselection.Selector) string {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "opus-4-6") || strings.Contains(lower, "opus_4_6") {
		return selector.ValidationFallbackID("claude-opus-4-1-20250805")
	}
	if strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "sonnet_4_6") {
		return selector.ValidationFallbackID("claude-sonnet-4-5-20250929")
	}
	if strings.Contains(lower, "sonnet-4-5") || strings.Contains(lower, "sonnet_4_5") {
		return selector.ValidationFallbackID("claude-sonnet-4-20250514")
	}
	return ""
}
