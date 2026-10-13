package providerconfig

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var managedKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ENABLE_TOOL_SEARCH", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION", "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME", "ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_ATTRIBUTION_HEADER", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS", "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS", "CC_HAHA_OPENAI_OAUTH_PROVIDER", "OPENAI_CODEX_OAUTH_FILE", "CC_HAHA_GROK_OAUTH_PROVIDER", "GROK_OAUTH_FILE",
	"CC_HAHA_IMAGE_PROVIDER_KIND", "CC_HAHA_IMAGE_PROVIDER_ID", "CC_HAHA_IMAGE_BASE_URL", "CC_HAHA_IMAGE_API_KEY", "CC_HAHA_IMAGE_MODEL",
}

func ManagedKeys() []string {
	keys := append([]string(nil), managedKeys...)
	var all []storage.Object
	_ = json.Unmarshal(presetJSON, &all)
	for _, preset := range all {
		for key := range object(preset["defaultEnv"]) {
			found := false
			for _, known := range keys {
				if key == known {
					found = true
					break
				}
			}
			if !found {
				keys = append(keys, key)
			}
		}
	}
	return keys
}
func capability(model, kind string, formats ...string) string {
	if len(formats) > 0 && formats[0] != "anthropic" && formats[0] != "openai_chat" && formats[0] != "openai_responses" {
		return "none"
	}
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimSuffix(strings.TrimSuffix(model, "[1m]"), ":1m")
	if at := strings.LastIndex(model, "/"); at >= 0 {
		model = model[at+1:]
	}
	generic := "thinking,effort,adaptive_thinking,xhigh_effort,max_effort"
	if len(formats) > 0 && formats[0] == "openai_responses" {
		return generic
	}
	switch {
	case model == "k3" || strings.HasPrefix(model, "k3-") || model == "kimi-k3" || strings.HasPrefix(model, "kimi-k3-") || strings.HasPrefix(model, "kimi-for-coding") || strings.HasPrefix(model, "kimi-k2."):
		return "thinking,required_thinking,effort,xhigh_effort,max_effort"
	case model == "glm-5.3" || model == "glm-5.3-flash":
		if kind == "zhipu_standard_api" {
			return "thinking,required_thinking,effort,max_effort"
		}
		return "thinking,required_thinking,effort,xhigh_effort,max_effort"
	case model == "glm-5.2" || strings.HasPrefix(model, "glm-5.2-") || strings.HasPrefix(model, "glm-4.") || model == "glm-5.1" || strings.HasPrefix(model, "glm-5.1-") || model == "glm-5-turbo" || strings.HasPrefix(model, "glm-5-turbo-"):
		return "thinking,effort,xhigh_effort,max_effort"
	default:
		return generic
	}
}
func oneMillion(model string, enabled bool) string {
	model = strings.TrimSpace(model)
	if !enabled {
		return model
	}
	lower := strings.ToLower(model)
	if strings.HasSuffix(lower, "[1m]") {
		model = model[:len(model)-4]
	} else if strings.HasSuffix(lower, ":1m") {
		model = model[:len(model)-3]
	}
	return strings.TrimSpace(model) + "[1m]"
}

type ManagedEnvironmentOptions struct {
	GrokRuntimeModelCatalog func() []providerproxy.GrokModel
	ServerPort              int
	ProxyPath               string
	ProxyReady              bool
}

// ProxyReady is supplied only by a host with the actual HTTP producer bound.
// A direct-only caller never silently sends Chat/Responses to Messages.
func BuildManagedEnv(provider storage.Object, options ...ManagedEnvironmentOptions) (map[string]string, error) {
	kind := str(provider, "runtimeKind")
	if kind != "" && kind != "anthropic_compatible" {
		return nil, unavailable("Go OAuth runtime is not implemented: " + kind)
	}
	preset := preset(str(provider, "presetId"))
	resolved := proxyConfig(provider)
	format := resolved.APIFormat
	needsProxy := format != "anthropic" || !resolved.SupportsNestedToolResultMedia
	proxy := ManagedEnvironmentOptions{}
	if len(options) > 0 {
		proxy = options[0]
	}
	if needsProxy && !proxy.ProxyReady {
		return nil, unavailable("Go provider protocol/media compatibility proxy is not implemented")
	}
	models, valid := normalizedModels(provider["models"])
	if !valid {
		return nil, bad("Invalid provider model mapping")
	}
	env := map[string]string{}
	for _, slot := range []string{"fable", "haiku", "sonnet", "opus"} {
		if model := str(models, slot); model != "" {
			env["ANTHROPIC_DEFAULT_"+strings.ToUpper(slot)+"_MODEL_SUPPORTED_CAPABILITIES"] = capability(model, str(preset, "reasoningProviderKind"), format)
		}
	}
	defaults := object(preset["defaultEnv"])
	for key, raw := range defaults {
		if strings.EqualFold(key, "ANTHROPIC_API_KEY") || strings.EqualFold(key, "ANTHROPIC_AUTH_TOKEN") {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			env[key] = value
		}
	}
	var output float64
	compat := object(provider["requestCompatibility"])
	if json.Unmarshal(compat["maxOutputTokens"], &output) == nil && output > 0 && output <= 9007199254740991 && output == math.Trunc(output) {
		env["CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS"] = string(encode(output))
	}
	if raw := provider["autoCompactWindow"]; len(raw) > 0 {
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = string(protocol.StringifyValidatedJavaScriptJSON(raw))
	}
	modelEnvironment, err := BuildModelContextEnvironment(provider)
	if err != nil {
		return nil, err
	}
	for key, value := range modelEnvironment {
		env[key] = value
	}
	if format == "anthropic" {
		env["ENABLE_TOOL_SEARCH"] = "false"
		if string(provider["toolSearchEnabled"]) == "true" {
			env["ENABLE_TOOL_SEARCH"] = "true"
		}
	}
	if string(provider["disableExperimentalBetas"]) == "true" {
		env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] = "1"
	}
	env["ANTHROPIC_BASE_URL"] = str(provider, "baseUrl")
	if needsProxy {
		port := proxy.ServerPort
		if port == 0 {
			port = 3456
		}
		path := proxy.ProxyPath
		if path == "" {
			path = "/proxy"
		}
		env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:" + strconv.Itoa(port) + path
	}
	key := str(provider, "apiKey")
	if key == "" {
		key = str(defaults, "ANTHROPIC_AUTH_TOKEN")
	}
	if key == "" {
		key = str(defaults, "ANTHROPIC_API_KEY")
	}
	strategy := str(provider, "authStrategy")
	if strategy == "" {
		strategy = str(preset, "authStrategy")
	}
	if strategy == "" {
		strategy = "auth_token"
	}
	if needsProxy {
		env["ANTHROPIC_API_KEY"] = "proxy-managed"
	} else {
		switch strategy {
		case "api_key":
			if key != "" {
				env["ANTHROPIC_API_KEY"] = key
			}
		case "auth_token", "auth_token_empty_api_key":
			env["ANTHROPIC_API_KEY"] = ""
			if key != "" {
				env["ANTHROPIC_AUTH_TOKEN"] = key
			}
		case "dual_same_token":
			if key != "" {
				env["ANTHROPIC_API_KEY"] = key
				env["ANTHROPIC_AUTH_TOKEN"] = key
			}
		case "dual_dummy":
			env["ANTHROPIC_API_KEY"] = "dummy"
			env["ANTHROPIC_AUTH_TOKEN"] = "dummy"
		default:
			return nil, bad("Invalid provider auth strategy")
		}
	}
	main := env["ANTHROPIC_MODEL"]
	if main != "" {
		env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(main)), "claude") {
			env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "1"
		}
	}
	if image := object(provider["imageGeneration"]); image != nil && strings.TrimSpace(str(image, "model")) != "" {
		env["CC_HAHA_IMAGE_PROVIDER_KIND"] = "openai_images"
		env["CC_HAHA_IMAGE_PROVIDER_ID"] = str(provider, "id")
		env["CC_HAHA_IMAGE_BASE_URL"] = str(image, "baseUrl")
		if env["CC_HAHA_IMAGE_BASE_URL"] == "" {
			env["CC_HAHA_IMAGE_BASE_URL"] = str(provider, "baseUrl")
		}
		env["CC_HAHA_IMAGE_API_KEY"] = str(image, "apiKey")
		if env["CC_HAHA_IMAGE_API_KEY"] == "" {
			env["CC_HAHA_IMAGE_API_KEY"] = str(provider, "apiKey")
		}
		env["CC_HAHA_IMAGE_MODEL"] = strings.TrimSpace(str(image, "model"))
	}
	return env, nil
}

func ResolveRuntimeEnv(ctx context.Context, store *storage.Store, configDir string, baseEnv map[string]string, selection *Selection, runtimeOptions ...ManagedEnvironmentOptions) (map[string]string, error) {
	options := Options{Store: store, ConfigDir: configDir}
	if len(runtimeOptions) > 0 {
		options.ServerPort = runtimeOptions[0].ServerPort
		options.ProxyReady = runtimeOptions[0].ProxyReady
		options.GrokRuntimeModelCatalog = runtimeOptions[0].GrokRuntimeModelCatalog
	}
	service, err := New(options)
	if err != nil {
		return nil, err
	}
	return service.ResolveRuntimeEnvironment(ctx, baseEnv, selection)
}

// ResolveRuntimeEnvironment preserves the shared provider/model/auth producer
// while allowing the ordinary CLI to retain its read-only index consumer.
func (service *Service) ResolveRuntimeEnvironment(ctx context.Context, baseEnv map[string]string, selection *Selection) (map[string]string, error) {
	result := map[string]string{}
	for key, value := range baseEnv {
		result[key] = value
	}
	if selection != nil {
		delete(result, "CC_HAHA_OPENAI_FAST_MODE")
	}
	var id string
	if selection == nil {
		index, err := service.ReadIndex(ctx)
		if err != nil {
			return nil, err
		}
		id = str(index, "activeId")
		if id == "" {
			return result, nil
		}
	} else if selection.ProviderID != nil {
		id = *selection.ProviderID
	}
	for _, key := range ManagedKeys() {
		delete(result, key)
	}
	if id != "" {
		provider, err := service.Provider(ctx, id)
		if err != nil {
			return nil, err
		}
		path := ""
		if selection != nil {
			path = "/proxy/providers/" + str(provider, "id")
		}
		env, err := service.buildManagedEnv(provider, path)
		if err != nil {
			return nil, err
		}
		for key, value := range env {
			result[key] = value
		}
	}
	if selection != nil && selection.ModelID != "" {
		result["ANTHROPIC_MODEL"] = selection.ModelID
		result["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(selection.ModelID)), "claude") {
			result["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "1"
		}
	}
	if id == "openai-official" && selection != nil && selection.OpenAIFastMode != nil && *selection.OpenAIFastMode {
		result["CC_HAHA_OPENAI_FAST_MODE"] = "1"
	}
	// EffortLevel is passed separately to the shared session/SDK control. Source
	// providerRuntimeEnv has no effort environment variable to invent here.
	return result, nil
}
