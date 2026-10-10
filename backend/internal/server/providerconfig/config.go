// Package providerconfig owns the existing cc-haha provider index and its
// managed environment. CLI and desktop use this same selection boundary.
package providerconfig

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Preset data is copied from src/server/config/providerPresets.json, including
// retired rows. Original preset work by Jason Young (cc-switch), MIT License.
//
//go:embed presets.json
var presetJSON []byte

var BuiltInIDs = []string{"claude-official", "openai-official", "grok-official"}

type Selection struct {
	ProviderID           *string
	ModelID, EffortLevel string
}
type Options struct {
	Store                   *storage.Store
	ConfigDir               string
	Managed                 *settings.ManagedService
	Settings                *settings.Service
	OpenAIModelCatalog      func(context.Context) ([]providerproxy.OpenAIModel, error)
	GrokModelCatalog        func(context.Context) ([]providerproxy.GrokModel, error)
	GrokRuntimeModelCatalog func() []providerproxy.GrokModel
	ClaudeRuntimeModel      func(context.Context, json.RawMessage) (*string, error)
	ServerPort              int
	ProxyReady              bool
}
type Service struct {
	options   Options
	indexRead func() (storage.Object, error)
}

func New(options Options) (*Service, error) {
	if options.Store == nil || !filepath.IsAbs(options.ConfigDir) {
		return nil, errors.New("provider service requires shared storage and an explicit absolute config directory")
	}
	return &Service{options: options}, nil
}
func str(object storage.Object, key string) string {
	var result string
	_ = json.Unmarshal(object[key], &result)
	return result
}
func object(raw json.RawMessage) storage.Object {
	var result storage.Object
	_ = json.Unmarshal(raw, &result)
	return result
}
func encode(value any) json.RawMessage { result, _ := json.Marshal(value); return result }
func clone(value storage.Object) storage.Object {
	result := storage.Object{}
	for key, raw := range value {
		result[key] = append(json.RawMessage(nil), raw...)
	}
	return result
}
func apiError(status int, code, message string) error {
	return &settings.APIError{StatusCode: status, Code: code, Message: message}
}
func bad(message string) error         { return apiError(400, "BAD_REQUEST", message) }
func unavailable(message string) error { return apiError(501, "GO_MIGRATION_NOT_READY", message) }
func builtIn(id string) bool           { return slices.Contains(BuiltInIDs, id) }
func (service *Service) indexPath() string {
	return filepath.Join(service.options.ConfigDir, "cc-haha", "providers.json")
}
func Presets() json.RawMessage { return append(json.RawMessage(nil), presetJSON...) }
func preset(id string) storage.Object {
	var all []storage.Object
	_ = json.Unmarshal(presetJSON, &all)
	for _, value := range all {
		if str(value, "id") == id {
			return value
		}
	}
	return storage.Object{}
}
func providers(index storage.Object) []storage.Object {
	var all []storage.Object
	_ = json.Unmarshal(index["providers"], &all)
	if all == nil {
		return []storage.Object{}
	}
	return all
}
func order(index storage.Object) []string {
	var original []json.RawMessage
	_ = json.Unmarshal(index["providerOrder"], &original)
	var ids []string
	for _, raw := range original {
		var id string
		if string(raw) != "null" && json.Unmarshal(raw, &id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func normalizedModels(raw json.RawMessage) (storage.Object, bool) {
	models := object(raw)
	if models == nil {
		return nil, false
	}
	for _, slot := range []string{"main", "haiku", "sonnet", "opus"} {
		var value string
		if json.Unmarshal(models[slot], &value) != nil || string(models[slot]) == "null" {
			return nil, false
		}
	}
	if raw, ok := models["fable"]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
			return nil, false
		}
	}
	main := trimModelJSON(models["main"])
	result := storage.Object{"main": main}
	for _, slot := range []string{"haiku", "sonnet", "opus"} {
		value := trimModelJSON(models[slot])
		if string(value) == `""` {
			value = main
		}
		result[slot] = value
	}
	if value := trimModelJSON(models["fable"]); value != nil && string(value) != `""` {
		result["fable"] = value
	}
	return result, true
}
func trimModelJSON(raw json.RawMessage) json.RawMessage {
	u, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	for len(u) > 0 && protocol.IsJavaScriptWhitespace(rune(u[0])) {
		u = u[1:]
	}
	for len(u) > 0 && protocol.IsJavaScriptWhitespace(rune(u[len(u)-1])) {
		u = u[:len(u)-1]
	}
	return protocol.QuoteJavaScriptUTF16(u)
}
func normalizedProvider(provider storage.Object) (storage.Object, bool) {
	if provider == nil {
		return nil, false
	}
	for _, key := range []string{"id", "presetId", "name", "apiKey", "baseUrl"} {
		var value string
		if json.Unmarshal(provider[key], &value) != nil || string(provider[key]) == "null" {
			return nil, false
		}
	}
	models, valid := normalizedModels(provider["models"])
	if !valid {
		return nil, false
	}
	if raw, ok := provider["model1mSupport"]; ok {
		value := object(raw)
		if value == nil {
			return nil, false
		}
		for _, slot := range []string{"main", "haiku", "sonnet", "opus"} {
			if string(value[slot]) != "true" && string(value[slot]) != "false" {
				return nil, false
			}
		}
	}
	if raw, ok := provider["imageGeneration"]; ok {
		image := object(raw)
		if image == nil {
			return nil, false
		}
		for _, key := range []string{"model", "baseUrl", "apiKey"} {
			value, exists := image[key]
			if key == "model" || exists {
				var text string
				if json.Unmarshal(value, &text) != nil || string(value) == "null" {
					return nil, false
				}
			}
		}
	}
	kind := str(provider, "runtimeKind")
	if _, present := provider["runtimeKind"]; present && kind != "anthropic_compatible" && kind != "openai_oauth" && kind != "grok_oauth" {
		return nil, false
	}
	result := clone(provider)
	result["models"] = encode(models)
	if len(result["apiFormat"]) == 0 || string(result["apiFormat"]) == "null" {
		result["apiFormat"] = encode("anthropic")
	}
	if kind == "" {
		result["runtimeKind"] = encode("anthropic_compatible")
	}
	result["toolSearchEnabled"] = encode(truthy(provider["toolSearchEnabled"], true))
	if raw, ok := provider["supportsNestedToolResultMedia"]; ok && string(raw) != "true" && string(raw) != "false" {
		delete(result, "supportsNestedToolResultMedia")
	}
	if support := object(provider["model1mSupport"]); support != nil {
		normalized := storage.Object{}
		enabled := false
		for _, slot := range []string{"main", "fable", "haiku", "sonnet", "opus"} {
			value := string(support[slot]) == "true"
			enabled = enabled || value
			normalized[slot] = encode(value)
		}
		if enabled {
			result["model1mSupport"] = encode(normalized)
		} else {
			delete(result, "model1mSupport")
		}
	}
	if truthy(provider["disableExperimentalBetas"], false) {
		result["disableExperimentalBetas"] = encode(true)
	} else {
		delete(result, "disableExperimentalBetas")
	}
	if image := object(provider["imageGeneration"]); image != nil {
		normalized := storage.Object{}
		for _, key := range []string{"model", "baseUrl", "apiKey"} {
			value := strings.TrimSpace(str(image, key))
			if value != "" {
				normalized[key] = encode(value)
			}
		}
		if str(normalized, "model") != "" {
			result["imageGeneration"] = encode(normalized)
		} else {
			delete(result, "imageGeneration")
		}
	}
	return result, true
}
func truthy(raw json.RawMessage, auto bool) bool {
	if string(raw) == "true" {
		return true
	}
	if string(raw) == "false" || len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return number != 0
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return false
	}
	text = strings.ToLower(strings.TrimSpace(text))
	return text == "1" || text == "true" || text == "on" || text == "yes" || (auto && (text == "auto" || strings.HasPrefix(text, "auto:")))
}
func normalize(raw json.RawMessage) (storage.Object, bool, error) {
	index := object(raw)
	if index == nil {
		return nil, false, nil
	}
	var original []json.RawMessage
	if json.Unmarshal(index["providers"], &original) != nil || original == nil {
		return nil, false, nil
	}
	result := clone(index)
	delete(result, "activeProviderId")
	version := float64(1)
	if raw := index["schemaVersion"]; len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &version)
	}
	result["schemaVersion"] = encode(version)
	all := []storage.Object{}
	for _, raw := range original {
		if provider, valid := normalizedProvider(object(raw)); valid {
			if version < 4 {
				provider["toolSearchEnabled"] = encode(false)
			}
			all = append(all, provider)
		}
	}
	result["providers"] = encode(all)
	var active string
	if json.Unmarshal(index["activeId"], &active) != nil || string(index["activeId"]) == "null" {
		active = str(index, "activeProviderId")
	}
	validActive := active == "openai-official" || active == "grok-official"
	for _, provider := range all {
		if str(provider, "id") == active {
			validActive = true
		}
	}
	if active == "" || !validActive {
		result["activeId"] = json.RawMessage("null")
	} else {
		result["activeId"] = encode(active)
	}
	known := []string{}
	for _, provider := range all {
		known = append(known, str(provider, "id"))
	}
	known = append(known, BuiltInIDs...)
	ids := []string{}
	for _, id := range append(order(index), known...) {
		if slices.Contains(known, id) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	result["providerOrder"] = encode(ids)
	official := object(index["officialProviderModels"])
	if official == nil {
		official = storage.Object{}
	}
	for _, id := range BuiltInIDs {
		if models, valid := normalizedModels(official[id]); valid {
			official[id] = encode(models)
		} else {
			delete(official, id)
		}
	}
	result["officialProviderModels"] = encode(official)
	return result, true, nil
}
func (service *Service) ReadIndex(context.Context) (storage.Object, error) {
	if service.indexRead != nil {
		return service.indexRead()
	}
	return storage.ReadRecoverableJSON(service.options.Store, storage.ReadOptions[storage.Object]{Path: service.indexPath(), Label: "providers index", Default: storage.Object{"schemaVersion": encode(6), "activeId": json.RawMessage("null"), "providers": json.RawMessage("[]"), "providerOrder": encode(BuiltInIDs), "officialProviderModels": json.RawMessage("{}")}, Normalize: normalize})
}
func (service *Service) Provider(ctx context.Context, id string) (storage.Object, error) {
	index, err := service.ReadIndex(ctx)
	if err != nil {
		return nil, err
	}
	if id == "openai-official" {
		return openAIOfficialProvider(index), nil
	}
	if id == "grok-official" {
		return grokOfficialProvider(index), nil
	}
	for _, provider := range providers(index) {
		if str(provider, "id") == id {
			return provider, nil
		}
	}
	return nil, apiError(404, "NOT_FOUND", "Provider not found: "+id)
}
func (service *Service) update(ctx context.Context, task func(storage.Object) error) error {
	return service.options.Store.WithWriteLock(service.indexPath(), func() error {
		index, err := service.ReadIndex(ctx)
		if err != nil {
			return err
		}
		if err = task(index); err != nil {
			return err
		}
		return service.options.Store.AtomicWriteJSON(service.indexPath(), "providers index", index, storage.WriteOptions{})
	})
}
func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&15 | 64
	bytes[8] = bytes[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

var mutableKeys = []string{"name", "apiKey", "authStrategy", "baseUrl", "apiFormat", "runtimeKind", "models", "model1mSupport", "autoCompactWindow", "modelContextWindows", "toolSearchEnabled", "disableExperimentalBetas", "supportsNestedToolResultMedia", "requestCompatibility", "imageGeneration", "notes"}

func validate(input storage.Object, create bool) error {
	if create {
		for _, key := range []string{"presetId", "name", "apiKey", "baseUrl", "models"} {
			if _, ok := input[key]; !ok {
				return bad("Missing provider field: " + key)
			}
		}
	}
	for _, key := range []string{"name", "apiKey", "baseUrl", "authStrategy", "apiFormat", "runtimeKind", "notes"} {
		if raw, ok := input[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
				return bad(key + " must be a string")
			}
			if key == "name" && value == "" {
				return bad(key + " must not be empty")
			}
		}
	}
	if create {
		var presetID string
		if json.Unmarshal(input["presetId"], &presetID) != nil || presetID == "" {
			return bad("presetId must be a nonempty string")
		}
	}
	if raw, ok := input["models"]; ok {
		if _, valid := normalizedModels(raw); !valid {
			return bad("models must contain main, haiku, sonnet, and opus strings")
		}
	}
	for key, allowed := range map[string][]string{"authStrategy": {"api_key", "auth_token", "auth_token_empty_api_key", "dual_same_token", "dual_dummy"}, "apiFormat": {"anthropic", "openai_chat", "openai_responses"}, "runtimeKind": {"anthropic_compatible", "openai_oauth", "grok_oauth"}} {
		if _, ok := input[key]; ok && !slices.Contains(allowed, str(input, key)) {
			return bad("Invalid " + key)
		}
	}
	for _, key := range []string{"toolSearchEnabled", "disableExperimentalBetas", "supportsNestedToolResultMedia"} {
		if raw, ok := input[key]; ok && string(raw) != "true" && string(raw) != "false" {
			return bad(key + " must be a boolean")
		}
	}
	for _, key := range []string{"model1mSupport", "autoCompactWindow", "modelContextWindows", "requestCompatibility", "imageGeneration"} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		if !create && string(raw) == "null" {
			continue
		}
		switch key {
		case "model1mSupport":
			support := object(raw)
			if support == nil {
				return bad("model1mSupport must be an object")
			}
			for _, slot := range []string{"main", "haiku", "sonnet", "opus"} {
				if string(support[slot]) != "true" && string(support[slot]) != "false" {
					return bad("model1mSupport." + slot + " must be a boolean")
				}
			}
			if value, ok := support["fable"]; ok && string(value) != "true" && string(value) != "false" {
				return bad("model1mSupport.fable must be a boolean")
			}
		case "autoCompactWindow":
			if !integerRange(raw, 16000, 10000000) {
				return bad("autoCompactWindow must be an integer between 16000 and 10000000")
			}
		case "modelContextWindows":
			windows := object(raw)
			if windows == nil {
				return bad("modelContextWindows must be an object")
			}
			for model, value := range windows {
				if model == "" || !integerRange(value, 16000, 10000000) {
					return bad("modelContextWindows entries must have a nonempty model and an integer between 16000 and 10000000")
				}
			}
		case "requestCompatibility":
			compat := object(raw)
			if compat == nil {
				return bad("requestCompatibility must be an object")
			}
			for _, field := range []string{"maxOutputTokens", "outputTokenLimit"} {
				if value, ok := compat[field]; ok && !integerRange(value, 1, 9007199254740991) {
					return bad("requestCompatibility." + field + " must be a positive safe integer")
				}
			}
			for field, allowed := range map[string][]string{"outputTokenField": {"auto", "max_tokens", "max_completion_tokens", "omit"}, "sampling": {"auto", "supported", "unsupported"}, "reasoning": {"auto", "supported", "unsupported"}, "parallelTools": {"auto", "supported", "unsupported"}, "structuredOutput": {"auto", "supported", "unsupported"}} {
				if _, ok := compat[field]; ok && !slices.Contains(allowed, str(compat, field)) {
					return bad("Invalid requestCompatibility." + field)
				}
			}
		case "imageGeneration":
			image := object(raw)
			if image == nil || strings.TrimSpace(str(image, "model")) == "" {
				return bad("imageGeneration.model must be a nonempty string")
			}
			for _, field := range []string{"model", "baseUrl", "apiKey"} {
				if value, ok := image[field]; ok {
					var text string
					if json.Unmarshal(value, &text) != nil || string(value) == "null" {
						return bad("imageGeneration." + field + " must be a string")
					}
				}
			}
		}
	}
	return nil
}
func integerRange(raw json.RawMessage, min, max float64) bool {
	var value float64
	return string(raw) != "null" && json.Unmarshal(raw, &value) == nil && value >= min && value <= max && math.Trunc(value) == value
}

// buildSavedProvider mirrors the original creation boundary. Read normalization
// is deliberately separate: it may drop legacy defaults but must not rewrite
// the newly created provider returned to the desktop.
func buildSavedProvider(input storage.Object) (storage.Object, error) {
	if err := validate(input, true); err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	p := storage.Object{"id": encode(id), "presetId": input["presetId"]}
	for _, key := range mutableKeys {
		if value, ok := input[key]; ok {
			p[key] = append(json.RawMessage(nil), value...)
		}
	}
	models, _ := normalizedModels(input["models"])
	p["models"] = encode(models)
	if _, ok := p["apiFormat"]; !ok {
		p["apiFormat"] = encode("anthropic")
	}
	if _, ok := p["runtimeKind"]; !ok {
		p["runtimeKind"] = encode("anthropic_compatible")
	}
	if _, ok := p["toolSearchEnabled"]; !ok {
		p["toolSearchEnabled"] = encode(false)
	}
	if string(p["disableExperimentalBetas"]) != "true" {
		delete(p, "disableExperimentalBetas")
	}
	if image := object(p["imageGeneration"]); image != nil {
		normalized := storage.Object{}
		for _, key := range []string{"model", "baseUrl", "apiKey"} {
			if v := trimModelJSON(image[key]); v != nil && string(v) != `""` {
				normalized[key] = v
			}
		}
		if len(normalized["model"]) > 0 {
			p["imageGeneration"] = encode(normalized)
		} else {
			delete(p, "imageGeneration")
		}
	}
	return p, nil
}
func appendSavedProvider(index storage.Object, p storage.Object) {
	all := providers(index)
	ids := order(index)
	saved := []string{}
	for _, item := range all {
		saved = append(saved, str(item, "id"))
	}
	insert, lastSaved := len(ids), -1
	for n, value := range ids {
		if slices.Contains(saved, value) {
			lastSaved = n
		}
	}
	if lastSaved >= 0 {
		insert = lastSaved + 1
	} else {
		for n, value := range ids {
			if builtIn(value) {
				insert = n
				break
			}
		}
	}
	ids = append(ids, "")
	copy(ids[insert+1:], ids[insert:])
	ids[insert] = str(p, "id")
	index["providers"] = encode(append(all, p))
	index["providerOrder"] = encode(ids)
}
func (service *Service) Add(ctx context.Context, input storage.Object) (storage.Object, error) {
	p, err := buildSavedProvider(input)
	if err != nil {
		return nil, err
	}
	err = service.update(ctx, func(index storage.Object) error { appendSavedProvider(index, p); return nil })
	return p, err
}

// ImportProviders performs one locked read and one atomic write. No failed
// member can leave a partially persisted cc-switch batch.
func (service *Service) ImportProviders(ctx context.Context, inputs []storage.Object) ([]storage.Object, error) {
	imported := []storage.Object{}
	if len(inputs) == 0 {
		return imported, nil
	}
	err := service.update(ctx, func(index storage.Object) error {
		for _, input := range inputs {
			p, err := buildSavedProvider(input)
			if err != nil {
				return err
			}
			appendSavedProvider(index, p)
			imported = append(imported, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return imported, nil
}
func (service *Service) Update(ctx context.Context, id string, input storage.Object) (storage.Object, error) {
	if err := validate(input, false); err != nil {
		return nil, err
	}
	var result storage.Object
	active := false
	err := service.update(ctx, func(index storage.Object) error {
		all := providers(index)
		for n, provider := range all {
			if str(provider, "id") != id {
				continue
			}
			result = clone(provider)
			for _, key := range mutableKeys {
				if raw, ok := input[key]; ok {
					if string(raw) == "null" || (key == "disableExperimentalBetas" && string(raw) == "false") {
						delete(result, key)
					} else {
						result[key] = raw
					}
				}
			}
			result, _ = normalizedProvider(result)
			all[n] = result
			active = str(index, "activeId") == id
			index["providers"] = encode(all)
			return nil
		}
		return apiError(404, "NOT_FOUND", "Provider not found: "+id)
	})
	if err == nil && active {
		err = service.sync(ctx, result)
	}
	return result, err
}
func (service *Service) Delete(ctx context.Context, id string) error {
	return service.update(ctx, func(index storage.Object) error {
		if str(index, "activeId") == id {
			return apiError(409, "CONFLICT", "Cannot delete the active provider. Switch to another provider first.")
		}
		all := providers(index)
		found := false
		remaining := []storage.Object{}
		for _, provider := range all {
			if str(provider, "id") == id {
				found = true
			} else {
				remaining = append(remaining, provider)
			}
		}
		if !found {
			return apiError(404, "NOT_FOUND", "Provider not found: "+id)
		}
		ids := []string{}
		for _, value := range order(index) {
			if value != id {
				ids = append(ids, value)
			}
		}
		index["providers"] = encode(remaining)
		index["providerOrder"] = encode(ids)
		return nil
	})
}
func permutation(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range a {
		if seen[id] || !slices.Contains(b, id) {
			return false
		}
		seen[id] = true
	}
	return true
}
func (service *Service) Reorder(ctx context.Context, ids []string) (storage.Object, error) {
	var result storage.Object
	err := service.update(ctx, func(index storage.Object) error {
		all := providers(index)
		saved := []string{}
		for _, provider := range all {
			saved = append(saved, str(provider, "id"))
		}
		full := append(append([]string(nil), saved...), BuiltInIDs...)
		newOrder := append([]string(nil), ids...)
		if !permutation(ids, full) {
			if !permutation(ids, saved) {
				return bad("orderedIds must be a permutation of existing provider ids and built-in provider ids")
			}
			newOrder = order(index)
			position := 0
			for n, id := range newOrder {
				if !builtIn(id) {
					newOrder[n] = ids[position]
					position++
				}
			}
		}
		sorted := []storage.Object{}
		for _, id := range newOrder {
			for _, provider := range all {
				if str(provider, "id") == id {
					sorted = append(sorted, provider)
				}
			}
		}
		index["providers"] = encode(sorted)
		index["providerOrder"] = encode(newOrder)
		result = storage.Object{"providers": index["providers"], "providerOrder": index["providerOrder"]}
		return nil
	})
	return result, err
}
func (service *Service) Activate(ctx context.Context, id string) error {
	provider, err := service.Provider(ctx, id)
	if err != nil {
		return err
	}
	if _, err = service.buildManagedEnv(provider); err != nil {
		return err
	}
	if service.options.Managed == nil {
		return unavailable("Provider activation requires the shared managed settings service")
	}
	if err = service.update(ctx, func(index storage.Object) error { index["activeId"] = encode(id); return nil }); err != nil {
		return err
	}
	if str(provider, "runtimeKind") == "openai_oauth" || str(provider, "runtimeKind") == "grok_oauth" {
		if err = service.sync(ctx, provider); err != nil {
			return err
		}
		_, err = settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
			current["model"] = object(provider["models"])["main"]
			delete(current, "modelContext")
			return settings.ManagedUpdate[struct{}]{Settings: current}, nil
		})
		return err
	}
	if str(provider, "presetId") == "official" {
		return service.clear(ctx)
	}
	return service.sync(ctx, provider)
}
func (service *Service) ActivateOfficial(ctx context.Context) error {
	if service.options.Managed == nil || service.options.Settings == nil {
		return unavailable("Official activation requires shared managed/user settings services")
	}
	if err := service.update(ctx, func(index storage.Object) error { index["activeId"] = json.RawMessage("null"); return nil }); err != nil {
		return err
	}
	if err := service.clear(ctx); err != nil {
		return err
	}
	index, err := service.ReadIndex(ctx)
	if err != nil {
		return err
	}
	models := object(object(index["officialProviderModels"])["claude-official"])
	if models == nil {
		models = storage.Object{"main": encode("claude-opus-5-5"), "fable": encode("claude-fable-5-1"), "haiku": encode("claude-haiku-4-5"), "sonnet": encode("claude-sonnet-5-5"), "opus": encode("claude-opus-5-5")}
	}
	return service.options.Settings.UpdateOfficialModelMapping(ctx, settings.ModelMapping{Main: str(models, "main"), Fable: str(models, "fable"), Haiku: str(models, "haiku"), Sonnet: str(models, "sonnet"), Opus: str(models, "opus")})
}
func (service *Service) sync(ctx context.Context, provider storage.Object) error {
	if service.options.Managed == nil {
		return unavailable("Provider sync requires the shared managed settings service")
	}
	env, err := service.buildManagedEnv(provider)
	if err != nil {
		return err
	}
	_, err = settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
		existing := object(current["env"])
		if existing == nil {
			existing = storage.Object{}
		}
		for _, key := range ManagedKeys() {
			delete(existing, key)
		}
		for key, value := range env {
			existing[key] = encode(value)
		}
		current["env"] = encode(existing)
		return settings.ManagedUpdate[struct{}]{Settings: current}, nil
	})
	return err
}
func (service *Service) clear(ctx context.Context) error {
	_, err := settings.UpdateManaged(ctx, service.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
		env := object(current["env"])
		for _, key := range ManagedKeys() {
			delete(env, key)
		}
		if len(env) == 0 {
			delete(current, "env")
		} else {
			current["env"] = encode(env)
		}
		return settings.ManagedUpdate[struct{}]{Settings: current}, nil
	})
	return err
}
