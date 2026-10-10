package providerconfig

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func grokOfficialProvider(index storage.Object) storage.Object {
	models := object(object(index["officialProviderModels"])["grok-official"])
	if models == nil {
		models = object(desktopoauth.EncodeProviderModels(desktopoauth.DefaultProviderModels("grok-official")))
	}
	windows := storage.Object{}
	for _, model := range providerproxy.GrokModelCatalog() {
		if model.ContextWindow > 0 {
			windows[model.Value] = encode(model.ContextWindow)
		}
	}
	return storage.Object{"id": encode("grok-official"), "presetId": encode("grok-official"), "name": encode("Grok Official"), "apiKey": encode(""), "authStrategy": encode("dual_dummy"), "baseUrl": encode(providerproxy.GrokCLIBaseURL), "apiFormat": encode("openai_chat"), "runtimeKind": encode("grok_oauth"), "models": encode(models), "modelContextWindows": encode(windows)}
}
func (s *Service) buildGrokManagedEnv(provider storage.Object) (map[string]string, error) {
	models := object(provider["models"])
	mapping := desktopoauth.ModelMapping{Main: str(models, "main"), Fable: str(models, "fable"), Haiku: str(models, "haiku"), Sonnet: str(models, "sonnet"), Opus: str(models, "opus")}
	windows := protocol.NewJSONObject()
	if s.options.GrokRuntimeModelCatalog != nil {
		for _, model := range s.options.GrokRuntimeModelCatalog() {
			if model.ContextWindow > 0 {
				key := encode(model.Value)
				value := encode(model.ContextWindow)
				if len(model.Raw) > 0 {
					var original protocol.JSONObject
					_ = original.UnmarshalJSON(model.Raw)
					key = original.Get("value")
					value = original.Get("contextWindow")
				}
				_ = windows.SetPropertyJSON(key, value)
			}
		}
	}
	env, err := desktopoauth.OfficialManagedEnvironment("grok-official", s.options.ConfigDir, mapping, &windows)
	if err != nil {
		return nil, err
	}
	raw, err := env.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var result map[string]string
	err = json.Unmarshal(raw, &result)
	return result, err
}
