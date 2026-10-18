package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
)

func (m *MainPrint) nativeMergeCcHahaEnvironment(legacy protocol.JSONObject) (protocol.JSONObject, error) {
	if m.nativeSettings == nil {
		return protocol.JSONObject{}, mainCap("saved provider environment actual settings owner")
	}
	base := map[string]string{}
	config, err := m.nativeCcHahaConfigDirectory()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	service, err := providerconfig.NewCLIRuntime(providerconfig.Options{Store: m.nativeSettings.store, ConfigDir: config})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	index, err := service.ReadIndex(context.Background())
	var active string
	_ = json.Unmarshal(index["activeId"], &active)
	if err != nil || active == "" {
		return legacy, nil
	}
	runtime := providerconfig.ManagedEnvironmentOptions{}
	selected, err := service.ProviderForProxy(context.Background(), "")
	if err != nil {
		return legacy, nil
	}
	if selected != nil && (selected.APIFormat != "anthropic" || !selected.SupportsNestedToolResultMedia) && !envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"]) {
		port, err := m.nativeEnsureStandaloneProxy(service)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		runtime = providerconfig.ManagedEnvironmentOptions{ServerPort: port, ProxyReady: true}
	}
	for _, field := range legacy.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		value, err := protocol.ExactJavaScriptStringJSON(field.Value)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var text string
		_ = json.Unmarshal(value, &text)
		base[key] = text
	}
	runtimeService, err := providerconfig.NewCLIRuntime(providerconfig.Options{Store: m.nativeSettings.store, ConfigDir: config, ServerPort: runtime.ServerPort, ProxyReady: runtime.ProxyReady})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	env, err := runtimeService.ResolveRuntimeEnvironment(context.Background(), base, nil)
	if err != nil {
		// readActiveProviderManagedEnv catches invalid/deleted records. Its
		// caller still applies the independently parsed legacy settings env.
		return legacy, nil
	}
	result := legacy.Clone()
	for _, key := range providerconfig.ManagedKeys() {
		if _, present := env[key]; !present {
			_ = result.DeletePropertyJSON(quoteMain(key))
		}
	}
	managed := map[string]bool{}
	for _, key := range providerconfig.ManagedKeys() {
		managed[key] = true
	}
	for key, value := range env {
		if managed[key] || result.Get(key) == nil {
			result.Set(key, quoteMain(value))
		}
	}
	return result, nil
}

func (m *MainPrint) nativeProcessEnvironment(scope *cooperative.Scope) (map[string]string, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	return m.nativeOpenAIEnvironment(), nil
}

func (m *MainPrint) nativeSettingsEnvironmentChanged(scope *cooperative.Scope) error {
	if err := m.Startup.ApplyConfigEnvironmentVariables(); err != nil {
		return err
	}
	// Proxy and mTLS plans are rebuilt by the original request stage from
	// these immutable values; neither has a separate native global memo.
	m.tls.Clear()
	env := m.nativeOpenAIEnvironment()
	hookEnv := map[string]string{}
	if value, present := env["CLAUDE_CODE_SIMPLE"]; present {
		hookEnv["CLAUDE_CODE_SIMPLE"] = value
	}
	if err := m.hook.SetOriginalEnvironment(hookEnv); err != nil {
		return err
	}
	return m.Host.SetNativeRequestEnvironment(scope, env)
}

func settingsEnvironmentReferenceChanged(before, after *protocol.JSONObject) bool {
	var old, next json.RawMessage
	if before != nil {
		old = before.Get("env")
	}
	if after != nil {
		next = after.Get("env")
	}
	if len(old) > 0 && (old[0] == '{' || old[0] == '[') || len(next) > 0 && (next[0] == '{' || next[0] == '[') {
		return true
	}
	if len(old) == 0 || len(next) == 0 {
		return len(old) != len(next)
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(old)) != string(protocol.StringifyValidatedJavaScriptJSON(next))
}
