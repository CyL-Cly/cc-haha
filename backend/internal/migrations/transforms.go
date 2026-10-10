package migrations

import (
	"encoding/json"
	"errors"
	"strings"
)

const CurrentProviderIndexSchemaVersion = 6

var builtInProviderIDs = []string{"claude-official", "openai-official", "grok-official"}

func validModels(raw json.RawMessage) bool {
	models, valid := parseObject(raw)
	if !valid {
		return false
	}
	for _, key := range []string{"main", "haiku", "sonnet", "opus"} {
		if !stringValue(models.get(key)) {
			return false
		}
	}
	return models.get("fable") == nil || stringValue(models.get("fable"))
}
func validSavedProvider(raw json.RawMessage) bool {
	provider, valid := parseObject(raw)
	if !valid {
		return false
	}
	for _, key := range []string{"id", "presetId", "name", "apiKey", "baseUrl"} {
		if !stringValue(provider.get(key)) {
			return false
		}
	}
	return validModels(provider.get("models"))
}
func validLegacyProvider(raw json.RawMessage) bool {
	provider, valid := parseObject(raw)
	if !valid {
		return false
	}
	for _, key := range []string{"id", "name", "apiKey", "baseUrl"} {
		if !stringValue(provider.get(key)) {
			return false
		}
	}
	models, valid := parseArray(provider.get("models"))
	if !valid {
		return false
	}
	for _, rawModel := range models {
		model, valid := parseObject(rawModel)
		if !valid || !stringValue(model.get("id")) {
			return false
		}
	}
	return true
}
func defaultOrder(providers []json.RawMessage) []json.RawMessage {
	result := []json.RawMessage{}
	for _, raw := range providers {
		provider, _ := parseObject(raw)
		if id := provider.get("id"); stringValue(id) {
			result = append(result, id)
		}
	}
	for _, id := range builtInProviderIDs {
		result = append(result, quote(id))
	}
	return result
}
func normalizeOrder(raw json.RawMessage, providers []json.RawMessage) json.RawMessage {
	defaults := defaultOrder(providers)
	known, seen := map[string]bool{}, map[string]bool{}
	for _, id := range defaults {
		known[stringKey(id)] = true
	}
	values, valid := parseArray(raw)
	if !valid {
		values = defaults
	}
	result := []json.RawMessage{}
	for _, id := range append(values, defaults...) {
		if !stringValue(id) {
			continue
		}
		key := stringKey(id)
		if known[key] && !seen[key] {
			seen[key] = true
			result = append(result, id)
		}
	}
	return arrayRaw(result)
}
func normalizeOfficialModels(raw json.RawMessage) json.RawMessage {
	value, valid := parseObject(raw)
	if !valid {
		return json.RawMessage("{}")
	}
	for _, id := range builtInProviderIDs {
		if !validModels(value.get(id)) {
			value.remove(id)
		}
	}
	return value.raw()
}
func migrateProviders(raw json.RawMessage) (json.RawMessage, error) {
	value, valid := parseObject(raw)
	providers, validArray := parseArray(value.get("providers"))
	if !valid || !validArray {
		result := object{}
		result.set("schemaVersion", json.RawMessage("6"))
		result.set("activeId", json.RawMessage("null"))
		result.set("providers", json.RawMessage("[]"))
		result.set("providerOrder", normalizeOrder(nil, nil))
		result.set("officialProviderModels", json.RawMessage("{}"))
		return result.raw(), nil
	}
	result := value.clone()
	for _, key := range []string{"activeProviderId", "providerOrder", "officialProviderModels"} {
		result.remove(key)
	}
	filtered := []json.RawMessage{}
	for _, provider := range providers {
		if !validSavedProvider(provider) {
			continue
		}
		if numberOr(value.get("schemaVersion"), 1) < 4 {
			item, _ := parseObject(provider)
			item.set("toolSearchEnabled", json.RawMessage("false"))
			provider = item.raw()
		}
		filtered = append(filtered, provider)
	}
	active := value.get("activeId")
	if !stringValue(active) {
		active = value.get("activeProviderId")
	}
	validActive := equalStrings(active, quote("openai-official")) || equalStrings(active, quote("grok-official"))
	for _, rawProvider := range filtered {
		provider, _ := parseObject(rawProvider)
		if equalStrings(provider.get("id"), active) {
			validActive = true
		}
	}
	if !truthy(active) || !stringValue(active) || !validActive {
		active = json.RawMessage("null")
	}
	result.set("schemaVersion", json.RawMessage("6"))
	result.set("activeId", active)
	result.set("providers", arrayRaw(filtered))
	result.set("providerOrder", normalizeOrder(value.get("providerOrder"), filtered))
	result.set("officialProviderModels", normalizeOfficialModels(value.get("officialProviderModels")))
	return result.raw(), nil
}

// Includes/regexp preserve JS runtime behavior for old, unvalidated env values.
// An array base URL has Array.includes; other non-string, non-array values
// throw. RegExp.test converts model values to strings (arrays join recursively).
func includesDeepSeek(raw json.RawMessage) (bool, error) {
	if isNull(raw) {
		return false, nil
	}
	if stringValue(raw) {
		var text string
		_ = json.Unmarshal(raw, &text)
		return strings.Contains(text, "api.deepseek.com"), nil
	}
	if values, valid := parseArray(raw); valid {
		for _, item := range values {
			if equalStrings(item, quote("api.deepseek.com")) {
				return true, nil
			}
		}
		return false, nil
	}
	return false, errors.New("baseUrl.includes is not a function")
}
func jsCoerceString(raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "null", nil
	}
	if stringValue(raw) {
		var result string
		_ = json.Unmarshal(raw, &result)
		return result, nil
	}
	if values, valid := parseArray(raw); valid {
		result := make([]string, len(values))
		for index, item := range values {
			if !isNull(item) {
				var err error
				result[index], err = jsCoerceString(item)
				if err != nil {
					return "", err
				}
			}
		}
		return strings.Join(result, ","), nil
	}
	if value, valid := parseObject(raw); valid {
		// JSON data cannot contain a callable own toString. Shadowing the
		// inherited method leaves valueOf returning an object, so ToPrimitive
		// throws instead of silently treating the model as a plain string.
		if value.get("toString") != nil {
			return "", errors.New("Cannot convert object to primitive value")
		}
		return "[object Object]", nil
	}
	return string(raw), nil
}
func migrateManaged(raw json.RawMessage) (json.RawMessage, error) {
	value, valid := parseObject(raw)
	if !valid {
		return json.RawMessage("{}"), nil
	}
	envRaw := value.get("env")
	if envRaw == nil {
		return value.raw(), nil
	}
	env, valid := parseObject(envRaw)
	if !valid {
		value.set("env", json.RawMessage("{}"))
		return value.raw(), nil
	}
	if !truthy(env.get("CC_HAHA_SEND_DISABLED_THINKING")) {
		return value.raw(), nil
	}
	deepSeek, err := includesDeepSeek(env.get("ANTHROPIC_BASE_URL"))
	if err != nil {
		return nil, err
	}
	if !deepSeek {
		for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
			model := env.get(key)
			if !truthy(model) {
				continue
			}
			text, err := jsCoerceString(model)
			if err != nil {
				return nil, err
			}
			// The source regexp has i but not u; non-ASCII case folds (e.g.
			// Kelvin sign -> K) must not turn unrelated model names into matches.
			text = strings.Map(func(r rune) rune {
				if r >= 'A' && r <= 'Z' {
					return r + ('a' - 'A')
				}
				return r
			}, text)
			if strings.HasPrefix(text, "deepseek-") || strings.HasPrefix(text, "deepseek_") {
				deepSeek = true
				break
			}
		}
	}
	if !deepSeek {
		return value.raw(), nil
	}
	env.remove("CC_HAHA_SEND_DISABLED_THINKING")
	for _, key := range []string{"ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"} {
		env.set(key, quote("thinking,effort,adaptive_thinking,xhigh_effort,max_effort"))
	}
	value.set("env", env.raw())
	return value.raw(), nil
}

func legacyModel(provider object, preferred json.RawMessage) json.RawMessage {
	models, _ := parseArray(provider.get("models"))
	if stringValue(preferred) {
		for _, raw := range models {
			model, _ := parseObject(raw)
			if equalStrings(model.get("id"), preferred) {
				return preferred
			}
		}
	}
	if len(models) > 0 {
		model, _ := parseObject(models[0])
		return model.get("id")
	}
	return quote("")
}
func migrateLegacy(raw json.RawMessage) json.RawMessage {
	value, valid := parseObject(raw)
	if !valid {
		return nil
	}
	source, valid := parseArray(value.get("providers"))
	if !valid {
		return nil
	}
	providers := []json.RawMessage{}
	active := json.RawMessage("null")
	for _, rawProvider := range source {
		if !validLegacyProvider(rawProvider) {
			continue
		}
		provider, _ := parseObject(rawProvider)
		main := legacyModel(provider, value.get("activeModel"))
		item := object{}
		item.set("id", provider.get("id"))
		item.set("presetId", quote("custom"))
		item.set("name", provider.get("name"))
		item.set("apiKey", provider.get("apiKey"))
		item.set("baseUrl", provider.get("baseUrl"))
		item.set("apiFormat", quote("anthropic"))
		models := object{}
		for _, key := range []string{"main", "haiku", "sonnet", "opus"} {
			models.set(key, main)
		}
		item.set("models", models.raw())
		if notes := provider.get("notes"); notes != nil {
			item.set("notes", notes)
		}
		providers = append(providers, item.raw())
		if bytesTrue(provider.get("isActive")) && isNull(active) {
			active = provider.get("id")
			continue
		}
		if stringValue(value.get("activeModel")) && isNull(active) {
			models, _ := parseArray(provider.get("models"))
			for _, rawModel := range models {
				model, _ := parseObject(rawModel)
				if equalStrings(model.get("id"), value.get("activeModel")) {
					active = provider.get("id")
					break
				}
			}
		}
	}
	if len(providers) == 0 {
		return nil
	}
	result := object{}
	result.set("schemaVersion", json.RawMessage("6"))
	result.set("activeId", active)
	result.set("providers", arrayRaw(providers))
	result.set("providerOrder", normalizeOrder(nil, providers))
	return result.raw()
}
func bytesTrue(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "true" }
func managedForLegacy(raw json.RawMessage) json.RawMessage {
	value, _ := parseObject(raw)
	active := value.get("activeId")
	if !stringValue(active) {
		return nil
	}
	providers, _ := parseArray(value.get("providers"))
	for _, rawProvider := range providers {
		provider, _ := parseObject(rawProvider)
		if !equalStrings(provider.get("id"), active) || !validModels(provider.get("models")) {
			continue
		}
		apiKey, baseURL := provider.get("apiKey"), provider.get("baseUrl")
		if !stringValue(apiKey) || !stringValue(baseURL) || !truthy(apiKey) || !truthy(baseURL) {
			return nil
		}
		models, _ := parseObject(provider.get("models"))
		env := object{}
		env.set("ANTHROPIC_BASE_URL", baseURL)
		env.set("ANTHROPIC_AUTH_TOKEN", apiKey)
		env.set("ANTHROPIC_MODEL", models.get("main"))
		if truthy(models.get("fable")) {
			env.set("ANTHROPIC_DEFAULT_FABLE_MODEL", models.get("fable"))
		}
		for _, pair := range [][2]string{{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "haiku"}, {"ANTHROPIC_DEFAULT_SONNET_MODEL", "sonnet"}, {"ANTHROPIC_DEFAULT_OPUS_MODEL", "opus"}} {
			env.set(pair[0], models.get(pair[1]))
		}
		result := object{}
		result.set("env", env.raw())
		return result.raw()
	}
	return nil
}
