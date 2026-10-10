package providerconfig

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// The built-in record takes precedence over saved records with the same ID,
// exactly as ProviderService.getProvider does. Only the alias mapping is saved.
func openAIOfficialProvider(index storage.Object) storage.Object {
	models := object(object(index["officialProviderModels"])["openai-official"])
	if models == nil {
		models = object(desktopoauth.EncodeProviderModels(desktopoauth.DefaultProviderModels("openai-official")))
	}
	windows := protocol.NewJSONObject()
	for _, model := range providerproxy.OpenAIModelCatalog() {
		if model.ContextWindow > 0 {
			windows.Set(model.Value, encode(model.ContextWindow))
		}
	}
	return storage.Object{
		"id": encode("openai-official"), "presetId": encode("openai-official"), "name": encode("ChatGPT Official"),
		"apiKey": encode(""), "authStrategy": encode("dual_dummy"), "baseUrl": encode("https://chatgpt.com/backend-api/codex"),
		"apiFormat": encode("openai_responses"), "runtimeKind": encode("openai_oauth"), "models": encode(models), "modelContextWindows": encode(windows),
	}
}

// OAuth paths belong to this service's explicit config directory. The pure
// direct-Anthropic helper cannot resolve them from an ambient user home.
func (service *Service) buildManagedEnv(provider storage.Object, proxyPaths ...string) (map[string]string, error) {
	if str(provider, "runtimeKind") == "grok_oauth" {
		return service.buildGrokManagedEnv(provider)
	}
	if str(provider, "runtimeKind") != "openai_oauth" {
		path := ""
		if len(proxyPaths) > 0 {
			path = proxyPaths[0]
		}
		return BuildManagedEnv(provider, ManagedEnvironmentOptions{ServerPort: service.options.ServerPort, ProxyReady: service.options.ProxyReady, ProxyPath: path})
	}
	models := object(provider["models"])
	mapping := desktopoauth.ModelMapping{Main: str(models, "main"), Fable: str(models, "fable"), Haiku: str(models, "haiku"), Sonnet: str(models, "sonnet"), Opus: str(models, "opus")}
	env, err := desktopoauth.OfficialManagedEnvironment("openai-official", service.options.ConfigDir, mapping, nil)
	if err != nil {
		return nil, err
	}
	data, err := env.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var result map[string]string
	err = json.Unmarshal(data, &result)
	return result, err
}
