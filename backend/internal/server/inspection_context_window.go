package server

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var inspectionProviderModelKeys = []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"}

func inspectionProviderAlias(value string) string {
	value = strings.ToLower(protocol.TrimJavaScriptString(value))
	if strings.HasSuffix(value, "[1m]") {
		value = strings.TrimSuffix(value, "[1m]")
	}
	if strings.HasSuffix(value, ":1m") {
		value = strings.TrimSuffix(value, ":1m")
	}
	return protocol.TrimJavaScriptString(value)
}
func inspectionProviderRelated(a, b string) bool {
	a, b = inspectionProviderAlias(a), inspectionProviderAlias(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if min(len(utf16.Encode([]rune(a))), len(utf16.Encode([]rune(b)))) < 6 {
		return false
	}
	for _, separator := range []string{"-", "_", ":", "/", ".", " "} {
		if strings.HasPrefix(a, b+separator) || strings.HasPrefix(b, a+separator) {
			return true
		}
	}
	return false
}
func (s *Server) inspectionProviders(ctx context.Context) (*providerconfig.Service, storage.Object) {
	service, err := providerconfig.New(providerconfig.Options{Store: s.options.Store, ConfigDir: s.options.ConfigDir, Settings: s.options.Settings, Managed: s.options.Managed})
	if err != nil {
		return nil, nil
	}
	index, err := service.ReadIndex(ctx)
	if err != nil {
		return service, nil
	}
	return service, index
}
func (s *Server) resolveInspectionContextWindow(ctx context.Context, model string, hint protocol.JSONObject, o modelaccounting.NativeCalculatorOptions) (float64, error) {
	service, index := s.inspectionProviders(ctx)
	ids := []string{}
	providerID := hint.Get("runtimeProviderId")
	if inspectionIsString(providerID) {
		ids = append(ids, hint.String("runtimeProviderId"))
	} else if string(providerID) != "null" {
		if active := stringField(index, "activeId"); active != "" {
			ids = append(ids, active)
		}
	}
	disabled := false
	switch strings.ToLower(protocol.TrimJavaScriptString(o.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"])) {
	case "1", "true", "yes", "on":
		disabled = true
	}
	capWindow := func(value float64) float64 {
		if value > 200000 && disabled {
			return 200000
		}
		return value
	}
	environment := func(id string) map[string]string {
		if service == nil {
			return nil
		}
		provider, err := service.Provider(ctx, id)
		if err != nil {
			return nil
		}
		env, err := providerconfig.BuildModelContextEnvironment(provider)
		if err != nil {
			return nil
		}
		return env
	}
	for _, id := range ids {
		env := environment(id)
		raw := env["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]
		if window, ok := modelaccounting.ConfiguredContextWindow(model, raw); ok {
			return capWindow(window), nil
		}
		if runtimeModel := hint.String("runtimeModelId"); runtimeModel != "" {
			if window, ok := modelaccounting.ConfiguredContextWindow(runtimeModel, raw); ok {
				return capWindow(window), nil
			}
		}
		for _, key := range inspectionProviderModelKeys {
			configured := env[key]
			if configured != "" && inspectionProviderRelated(model, configured) {
				if window, ok := modelaccounting.ConfiguredContextWindow(configured, raw); ok {
					return capWindow(window), nil
				}
			}
		}
	}
	if len(providerID) == 0 {
		var saved []storage.Object
		_ = json.Unmarshal(index["providers"], &saved)
		matches := map[float64]bool{}
		for _, provider := range saved {
			env := environment(stringField(provider, "id"))
			raw := env["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]
			if window, ok := modelaccounting.ConfiguredContextWindow(model, raw); ok {
				matches[window] = true
				continue
			}
			for _, key := range inspectionProviderModelKeys {
				configured := env[key]
				if configured != "" && inspectionProviderRelated(model, configured) {
					if window, ok := modelaccounting.ConfiguredContextWindow(configured, raw); ok {
						matches[window] = true
						break
					}
				}
			}
		}
		if len(matches) == 1 {
			for window := range matches {
				return capWindow(window), nil
			}
		}
	}
	return (modelaccounting.RestoredModelResolver{Environment: o.Environment, Settings: o.Settings, Global: o.Global, SDKBetas: o.SDKBetas}).ContextWindow(model)
}
