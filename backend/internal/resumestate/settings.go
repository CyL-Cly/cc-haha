package resumestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func members(value protocol.JSONObject) []protocol.JSONField {
	raw, _ := value.MarshalJSON()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	result := []protocol.JSONField{}
	for decoder.More() {
		token, _ := decoder.Token()
		var raw json.RawMessage
		_ = decoder.Decode(&raw)
		result = append(result, protocol.JSONField{Name: token.(string), Value: raw})
	}
	return result
}
func merge(target, source protocol.JSONObject) protocol.JSONObject {
	if !target.IsObject() {
		target = protocol.NewJSONObject()
	}
	for _, field := range members(source) {
		prior := target.Get(field.Name)
		if object(prior).IsObject() && object(field.Value).IsObject() {
			target.Set(field.Name, encode(merge(object(prior), object(field.Value))))
		} else if old, ok := array(prior); ok {
			if next, ok := array(field.Value); ok {
				for _, item := range next {
					duplicate := false
					for _, existing := range old {
						if scalarEqual(existing, item) {
							duplicate = true
						}
					}
					if !duplicate {
						old = append(old, item)
					}
				}
				target.Set(field.Name, encode(old))
			} else {
				target.Set(field.Name, field.Value)
			}
		} else {
			target.Set(field.Name, field.Value)
		}
	}
	return target
}
func (service *Service) readSettings(path string) (protocol.JSONObject, error) {
	data, err := service.options.Files.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
		return protocol.NewJSONObject(), nil
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value := object(protocol.DecodeJavaScriptUTF8(data))
	if !value.IsObject() {
		return protocol.JSONObject{}, &CapabilityError{Capability: "CLI settings validation/error recovery"}
	}
	if err := validateSettingsProfile(value); err != nil {
		return protocol.JSONObject{}, err
	}
	return value, nil
}

// This finite file-policy profile does not impersonate the full SettingsSchema.
// Unknown or unsupported settings are rejected before restoration effects.
func validateSettingsProfile(value protocol.JSONObject) error {
	if !value.IsObject() {
		return &CapabilityError{Capability: "CLI settings validation/error recovery"}
	}
	for _, field := range members(value) {
		if strings.ContainsRune(field.Name, utf8.RuneError) {
			return &CapabilityError{Capability: "CLI settings UTF-16 property identity"}
		}
		switch field.Name {
		case "plansDirectory":
			if _, ok := units(field.Value); !ok {
				return &CapabilityError{Capability: "invalid CLI setting plansDirectory"}
			}
		case "disableAllHooks", "allowManagedHooksOnly":
			if string(field.Value) != "true" && string(field.Value) != "false" {
				return &CapabilityError{Capability: "invalid CLI setting " + field.Name}
			}
		case "cleanupPeriodDays":
			if len(field.Value) == 0 || field.Value[0] != '-' && (field.Value[0] < '0' || field.Value[0] > '9') || jsonNumber(field.Value) < 0 || jsonNumber(field.Value) > 9007199254740991 || math.IsInf(jsonNumber(field.Value), 0) || math.Trunc(jsonNumber(field.Value)) != jsonNumber(field.Value) {
				return &CapabilityError{Capability: "invalid CLI setting cleanupPeriodDays"}
			}
		case "hooks":
			if err := validateHookSettings(field.Value); err != nil {
				return err
			}
		case "enabledPlugins":
			if !object(field.Value).IsObject() {
				return &CapabilityError{Capability: "invalid CLI setting enabledPlugins"}
			}
			for _, entry := range members(object(field.Value)) {
				if strings.ContainsRune(entry.Name, utf8.RuneError) {
					return &CapabilityError{Capability: "CLI plugin UTF-16 property identity"}
				}
				if string(entry.Value) != "true" && string(entry.Value) != "false" {
					return &CapabilityError{Capability: "invalid CLI plugin enablement"}
				}
			}
		case "extraKnownMarketplaces":
			if nonempty(field.Value) || !object(field.Value).IsObject() {
				return &CapabilityError{Capability: "CLI marketplace settings schema"}
			}
		default:
			return &CapabilityError{Capability: "CLI setting outside validated resume profile: " + field.Name}
		}
	}
	return nil
}
func validateHookSettings(raw json.RawMessage) error {
	if !object(raw).IsObject() {
		return &CapabilityError{Capability: "invalid CLI hooks settings"}
	}
	for _, event := range members(object(raw)) {
		if event.Name != "SessionStart" {
			return &CapabilityError{Capability: "CLI hook schema outside SessionStart profile"}
		}
		matchers, ok := array(event.Value)
		if !ok {
			return &CapabilityError{Capability: "invalid CLI SessionStart matchers"}
		}
		for _, rawMatcher := range matchers {
			matcher := object(rawMatcher)
			if !matcher.IsObject() {
				return &CapabilityError{Capability: "invalid CLI SessionStart matcher"}
			}
			for _, field := range members(matcher) {
				if field.Name == "matcher" {
					if _, ok := units(field.Value); !ok {
						return &CapabilityError{Capability: "invalid CLI hook matcher string"}
					}
				} else if field.Name != "hooks" {
					return &CapabilityError{Capability: "CLI hook matcher outside validated profile"}
				}
			}
			hooks, ok := array(matcher.Get("hooks"))
			if !ok {
				return &CapabilityError{Capability: "invalid CLI hook list"}
			}
			for _, rawHook := range hooks {
				hook := object(rawHook)
				if hook.String("type") != "command" {
					return &CapabilityError{Capability: "CLI hook type schema outside command profile"}
				}
				if _, ok := units(hook.Get("command")); !ok {
					return &CapabilityError{Capability: "invalid CLI hook command"}
				}
				for _, field := range members(hook) {
					if field.Name != "type" && field.Name != "command" {
						return &CapabilityError{Capability: "CLI command hook outside validated profile"}
					}
				}
			}
		}
	}
	return nil
}
func (service *Service) settings() (merged, policy protocol.JSONObject, err error) {
	if cached := service.options.SettingsState.GetSessionSettingsCache(); cached != nil && cached.Settings != nil {
		merged = cached.Settings.Clone()
		if err := validateSettingsProfile(merged); err != nil {
			return protocol.JSONObject{}, protocol.JSONObject{}, err
		}
		policy = protocol.NewJSONObject()
		if value, exists := service.options.SettingsState.GetCachedSettingsForSource("policySettings"); exists && value != nil {
			policy = value.Clone()
			if err := validateSettingsProfile(policy); err != nil {
				return protocol.JSONObject{}, protocol.JSONObject{}, err
			}
		}
		return merged, policy, nil
	}
	merged, policy = protocol.NewJSONObject(), protocol.NewJSONObject()
	for _, path := range []string{filepath.Join(service.options.ConfigDir, "settings.json"), filepath.Join(service.options.OriginalCWD, ".claude", "settings.json"), filepath.Join(service.options.OriginalCWD, ".claude", "settings.local.json")} {
		value, err := service.readSettings(path)
		if err != nil {
			return protocol.JSONObject{}, protocol.JSONObject{}, err
		}
		merged = merge(merged, value)
	}
	for _, path := range service.options.PolicyPaths {
		value, err := service.readSettings(path)
		if err != nil {
			return protocol.JSONObject{}, protocol.JSONObject{}, err
		}
		policy = merge(policy, value)
	}
	return merge(merged, policy), policy, nil
}

// Reject capabilities this component cannot execute before Load starts plan,
// history or skill restoration. Source's genuine bare/managed-disabled paths
// still resolve from the original validated settings snapshot.
func (service *Service) validateResolvedProfile(merged, policy protocol.JSONObject) error {
	if envTruthy(service.options.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	for _, argument := range service.options.Arguments {
		if argument == "--bare" {
			return nil
		}
	}
	managedOnly := string(policy.Get("allowManagedHooksOnly")) == "true" || string(merged.Get("disableAllHooks")) == "true" && string(policy.Get("disableAllHooks")) != "true"
	if !managedOnly {
		for _, argument := range service.options.Arguments {
			if argument == "--plugin-dir" || strings.HasPrefix(argument, "--plugin-dir=") || argument == "--settings" || strings.HasPrefix(argument, "--settings=") {
				return &CapabilityError{Capability: "SessionStart CLI flag plugins/settings"}
			}
		}
		if nonempty(merged.Get("enabledPlugins")) || nonempty(merged.Get("extraKnownMarketplaces")) || envTruthy(service.options.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"]) {
			return &CapabilityError{Capability: "SessionStart plugin discovery/execution"}
		}
	}
	if string(policy.Get("disableAllHooks")) != "true" {
		hooks := merged.Get("hooks")
		if managedOnly {
			hooks = policy.Get("hooks")
		}
		if nonempty(object(hooks).Get("SessionStart")) {
			return &CapabilityError{Capability: "active SessionStart hook service"}
		}
	}
	return nil
}
func nonempty(raw json.RawMessage) bool {
	if value := object(raw); value.IsObject() {
		return len(members(value)) > 0
	}
	if values, ok := array(raw); ok {
		return len(values) > 0
	}
	return truthy(raw)
}
func (service *Service) sessionStart(sid json.RawMessage) error {
	if envTruthy(service.options.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	for _, argument := range service.options.Arguments {
		if argument == "--bare" {
			return nil
		}
	}
	merged, policy, err := service.settings()
	if err != nil {
		return err
	}
	if err := service.validateResolvedProfile(merged, policy); err != nil {
		return err
	}
	managedOnly := string(policy.Get("allowManagedHooksOnly")) == "true" || string(merged.Get("disableAllHooks")) == "true" && string(policy.Get("disableAllHooks")) != "true"
	if managedOnly {
		service.diagnostic("debug", "Skipping plugin hooks - allowManagedHooksOnly is enabled", nil)
	} else {
		service.options.Now()
		for _, file := range []string{"known_marketplaces.json", "installed_plugins.json"} {
			path := filepath.Join(service.options.ConfigDir, "plugins", file)
			data, err := service.options.Files.ReadFile(path)
			value := protocol.NewJSONObject()
			if errors.Is(err, fs.ErrNotExist) {
				if file == "installed_plugins.json" {
					service.diagnostic("debug", "installed_plugins.json doesn't exist, returning empty V2 object", nil)
				}
				err = nil
			} else if err == nil {
				value = object(protocol.DecodeJavaScriptUTF8(data))
				if !value.IsObject() {
					return &CapabilityError{Capability: "CLI plugin registry validation"}
				}
			}
			if err != nil {
				return err
			}
			if nonempty(value.Get("plugins")) || file == "known_marketplaces.json" && len(members(value)) > 0 {
				return &CapabilityError{Capability: "SessionStart configured plugin registry"}
			}
		}
		service.diagnostic("debug", "Found 0 plugins (0 enabled, 0 disabled)", nil)
		service.diagnostic("debug", "Registered 0 hooks from 0 plugins", nil)
		service.options.Now()
	}
	// executeSessionStartHooks allocates its tool-use ID before executeHooks'
	// genuine managed-disable or no-matching-config early return.
	service.options.UUID()
	if string(policy.Get("disableAllHooks")) == "true" {
		return nil
	}
	service.diagnostic("debug", "Getting matching hook commands for SessionStart with query: resume", []byte(`{"level":"verbose"}`))
	service.diagnostic("debug", "Found 0 hook matchers in settings", []byte(`{"level":"verbose"}`))
	return nil
}
