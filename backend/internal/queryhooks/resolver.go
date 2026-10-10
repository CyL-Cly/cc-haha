package queryhooks

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type resolverState struct {
	loaded      bool
	hooks       *protocol.JSONObject
	disabled    bool
	managedOnly bool
	snapshot    *protocol.JSONObject
}

// load resolves original file sources through shared CLI parsing and caching.
// Its finite schema has only empty hook arrays; insertion order is retained.
func (service *Service) load() error {
	if service.resolver.loaded {
		return nil
	}
	if service.options.PluginHooks != nil {
		plugins, err := service.options.PluginHooks()
		if err != nil {
			return err
		}
		for _, hooks := range plugins {
			for _, event := range hooks.OwnProperties() {
				var matchers []protocol.JSONObject
				if json.Unmarshal(event.Value, &matchers) != nil {
					return unsupported("plugin hook matchers")
				}
				for _, matcher := range matchers {
					var values []json.RawMessage
					if json.Unmarshal(matcher.Get("hooks"), &values) != nil || len(values) != 0 {
						return unsupported("configured plugin hook execution")
					}
				}
			}
		}
	}
	if service.options.MergedSettings != nil {
		settings, err := service.options.MergedSettings()
		if err != nil {
			return err
		}
		if settings == nil {
			return unsupported("shared merged settings identity")
		}
		disabled := string(settings.Get("disableAllHooks")) == "true"
		managedOnly := false
		selected := settings
		if service.options.Commands != nil {
			policy := protocol.NewJSONObject()
			if service.options.PolicySettings != nil {
				p, e := service.options.PolicySettings()
				if e != nil {
					return e
				}
				if p != nil {
					policy = p.Clone()
				}
			}
			disabled = string(policy.Get("disableAllHooks")) == "true"
			managedOnly = string(policy.Get("allowManagedHooksOnly")) == "true" || string(settings.Get("disableAllHooks")) == "true" && !disabled
			restricted := string(policy.Get("strictPluginOnlyCustomization")) == "true"
			var names []string
			if json.Unmarshal(policy.Get("strictPluginOnlyCustomization"), &names) == nil {
				for _, n := range names {
					if n == "hooks" {
						restricted = true
					}
				}
			}
			if managedOnly || restricted {
				selected = &policy
			}
		}
		var hooks *protocol.JSONObject
		// Source getHooksFromAllowedSources returns managed-only hooks before
		// parsing non-managed matchers. This admitted host has an absent policy.
		if !disabled {
			if raw := selected.Get("hooks"); raw != nil {
				var incoming protocol.JSONObject
				if err := json.Unmarshal(raw, &incoming); err != nil || !incoming.IsObject() {
					return unsupported("parsed hooks object")
				}
				for _, field := range incoming.OwnProperties() {
					var matchers []json.RawMessage
					if err := json.Unmarshal(field.Value, &matchers); err != nil || service.options.Commands == nil && len(matchers) != 0 {
						return unsupported("configured hook matchers")
					}
				}
				hooks = &incoming
			}
		}
		service.resolver.loaded, service.resolver.hooks, service.resolver.disabled = true, hooks, disabled
		service.resolver.managedOnly = managedOnly
		return nil
	}
	var hooks *protocol.JSONObject
	disabled := false
	for _, source := range []string{"userSettings", "projectSettings", "localSettings"} {
		settings, err := service.options.Settings.GetEditableSource(source, "")
		if err != nil {
			var capability *clisettings.CapabilityError
			if errors.As(err, &capability) {
				return unsupported("original settings: " + capability.Capability)
			}
			return err
		}
		if settings == nil {
			continue
		}
		if raw := settings.Get("disableAllHooks"); raw != nil {
			disabled = string(raw) == "true"
		}
		if raw := settings.Get("hooks"); raw != nil {
			var incoming protocol.JSONObject
			if err := json.Unmarshal(raw, &incoming); err != nil || !incoming.IsObject() {
				return unsupported("parsed hooks object")
			}
			if hooks == nil {
				empty := protocol.NewJSONObject()
				hooks = &empty
			}
			for _, field := range incoming.OwnProperties() {
				var matchers []json.RawMessage
				if err := json.Unmarshal(field.Value, &matchers); err != nil || len(matchers) != 0 {
					return unsupported("configured hook matchers")
				}
				if err := hooks.SetPropertyJSON(field.NameJSON, field.Value); err != nil {
					return err
				}
			}
		}
	}
	service.resolver.loaded, service.resolver.hooks, service.resolver.disabled = true, hooks, disabled
	return nil
}
func (service *Service) snapshot() (*protocol.JSONObject, error) {
	if service.resolver.snapshot != nil {
		return service.resolver.snapshot, nil
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	value := service.resolver.hooks
	if service.resolver.disabled || value == nil {
		empty := protocol.NewJSONObject()
		value = &empty
	}
	service.resolver.snapshot = value
	return value, nil
}
func (service *Service) Snapshot() (*protocol.JSONObject, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.snapshot()
}

// ResetSnapshot does not invalidate Source's settings cache. If merged hooks
// were already loaded, capture can return the identical original object again.
func (service *Service) ResetSnapshot() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.resolver.snapshot = nil
	// Source resetHooksConfigSnapshot also resets SDK init state. In this
	// initial-null JSON-schema profile its observable registry is cleared;
	// session and programmatic hooks belong to different stores.
	service.registry.registered, service.registry.order = nil, nil
	service.clearSDKCallbackMatchers()
}
func (service *Service) UpdateSnapshot() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.options.SettingsState.ResetSettingsCache()
	service.resolver = resolverState{}
	_, err := service.snapshot()
	return err
}
func (service *Service) exists(event, sid string) (bool, error) {
	snapshot, err := service.snapshot()
	if err != nil {
		return false, err
	}
	var matchers []json.RawMessage
	if raw := snapshot.Get(event); raw != nil {
		if err := json.Unmarshal(raw, &matchers); err != nil {
			return false, unsupported("mutated hook snapshot")
		}
		if len(matchers) > 0 {
			return true, nil
		}
	}
	if service.registry.registered != nil && len(service.registry.registered[event]) > 0 {
		return true, nil
	}
	if len(service.nativeCommandMatchers(event, sid)) > 0 {
		return true, nil
	}
	if len(service.sdkCallbackMatchers(event)) > 0 {
		return true, nil
	}
	if store := service.registry.sessions[sid]; store != nil {
		_, exists := store.events[event]
		if exists {
			return true, nil
		}
	}
	return false, nil
}
func (service *Service) active(event, sid string) bool {
	// Snapshot references are mutable just like Source. A later active matcher
	// must not silently pass through the finite empty execution capability.
	if snapshot := service.resolver.snapshot; snapshot != nil {
		var matchers []json.RawMessage
		if raw := snapshot.Get(event); raw != nil && (json.Unmarshal(raw, &matchers) != nil || len(matchers) > 0) {
			return true
		}
	}
	// Managed-only discards session hooks, but SDK registered callbacks remain.
	for _, matcher := range service.registry.registered[event] {
		if len(matcher.Hooks) != 0 {
			return true
		}
	}
	if !service.resolver.disabled && !service.resolver.managedOnly {
		if store := service.registry.sessions[sid]; store != nil {
			for _, matcher := range store.events[event] {
				if len(matcher.Hooks) != 0 {
					return true
				}
			}
		}
	}
	return false
}
