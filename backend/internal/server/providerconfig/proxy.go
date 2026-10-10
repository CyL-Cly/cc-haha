package providerconfig

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// ProviderForProxy reads the same recoverable provider index as the UI. Scoped
// missing providers retain the original error; a missing active provider is a
// null selection. Official OAuth providers have their own SDK fetch boundary.
func (service *Service) ProviderForProxy(ctx context.Context, id string) (*providerproxy.ProxyConfig, error) {
	scoped := id != ""
	if !scoped {
		index, err := service.ReadIndex(ctx)
		if err != nil {
			return nil, err
		}
		id = str(index, "activeId")
		if id == "" {
			return nil, nil
		}
	}
	if id == "openai-official" || id == "grok-official" {
		return nil, nil
	}
	index, err := service.ReadIndex(ctx)
	if err != nil {
		if !scoped {
			return nil, nil
		}
		return nil, err
	}
	var selected storage.Object
	for _, p := range providers(index) {
		if str(p, "id") == id {
			selected = p
			break
		}
	}
	if selected == nil {
		if !scoped {
			return nil, nil
		}
		return nil, apiError(404, "NOT_FOUND", "Provider not found: "+id)
	}
	return proxyConfig(selected), nil
}

func proxyConfig(p storage.Object) *providerproxy.ProxyConfig {
	preset := preset(str(p, "presetId"))
	defaults := object(preset["defaultEnv"])
	key := str(p, "apiKey")
	if key == "" {
		key = str(defaults, "ANTHROPIC_AUTH_TOKEN")
	}
	if key == "" {
		key = str(defaults, "ANTHROPIC_API_KEY")
	}
	format := str(p, "apiFormat")
	if len(p["apiFormat"]) == 0 || string(p["apiFormat"]) == "null" {
		format = "anthropic"
	}
	rules := preset["modelApiFormats"]
	var list []json.RawMessage
	_ = json.Unmarshal(rules, &list)
	if len(list) > 0 && len(preset["apiFormat"]) > 0 && string(preset["apiFormat"]) != "null" {
		format = str(preset, "apiFormat")
	}
	if len(rules) == 0 || string(rules) == "null" {
		rules = json.RawMessage("[]")
	}
	headers := preset["upstreamHeaders"]
	if len(headers) == 0 || string(headers) == "null" {
		headers = json.RawMessage("{}")
	}
	strategy := str(p, "authStrategy")
	if len(p["authStrategy"]) == 0 || string(p["authStrategy"]) == "null" {
		strategy = str(preset, "authStrategy")
		if len(preset["authStrategy"]) == 0 || string(preset["authStrategy"]) == "null" {
			strategy = "auth_token"
		}
	}
	return &providerproxy.ProxyConfig{ID: str(p, "id"), Name: str(p, "name"), BaseURL: str(p, "baseUrl"), APIKey: key, APIFormat: format, AuthStrategy: strategy, SupportsNestedToolResultMedia: string(p["supportsNestedToolResultMedia"]) != "false", ModelAPIFormats: append(json.RawMessage(nil), rules...), UpstreamHeaders: append(json.RawMessage(nil), headers...), RequestCompatibility: append(json.RawMessage(nil), p["requestCompatibility"]...)}
}
