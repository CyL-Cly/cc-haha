package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// MainCommand borrows the same saved-provider/storage consumers as MainPrint.
// It owns only the ordinary command's optional loopback listener, never an ASK.
type mainCommandProviderProxy struct {
	mu      sync.Mutex
	service *providerproxytransport.Standalone
	closed  bool
	ready   chan struct{}
}

func (m *MainCommand) mergeCLIProviderEnvironment(legacy protocol.JSONObject) (protocol.JSONObject, error) {
	config, err := m.Settings.nativeCcHahaConfigDirectory()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	service, err := providerconfig.NewCLIRuntime(providerconfig.Options{Store: m.Settings.store, ConfigDir: config})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	index, err := service.ReadIndex(context.Background())
	var active string
	_ = json.Unmarshal(index["activeId"], &active)
	if err != nil || active == "" {
		return legacy, nil
	}
	selected, err := service.ProviderForProxy(context.Background(), "")
	if err != nil {
		return legacy, nil
	}
	runtime := providerconfig.ManagedEnvironmentOptions{}
	if selected != nil && (selected.APIFormat != "anthropic" || !selected.SupportsNestedToolResultMedia) && !envTruthyNative(m.Native.Environment["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"]) {
		port, err := m.ensureCLIProviderProxy(service)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		runtime = providerconfig.ManagedEnvironmentOptions{ServerPort: port, ProxyReady: true}
	}
	base := map[string]string{}
	for _, field := range legacy.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		raw, err := protocol.ExactJavaScriptStringJSON(field.Value)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var text string
		_ = json.Unmarshal(raw, &text)
		base[key] = text
	}
	env, err := func() (map[string]string, error) {
		runtimeService, err := providerconfig.NewCLIRuntime(providerconfig.Options{Store: m.Settings.store, ConfigDir: config, ServerPort: runtime.ServerPort, ProxyReady: runtime.ProxyReady})
		if err != nil {
			return nil, err
		}
		return runtimeService.ResolveRuntimeEnvironment(context.Background(), base, nil)
	}()
	if err != nil {
		return legacy, nil
	}
	result := legacy.Clone()
	managed := map[string]bool{}
	for _, key := range providerconfig.ManagedKeys() {
		managed[key] = true
		if _, present := env[key]; !present {
			_ = result.DeletePropertyJSON(quoteMain(key))
		}
	}
	for key, value := range env {
		if managed[key] || result.Get(key) == nil {
			result.Set(key, quoteMain(value))
		}
	}
	return result, nil
}

func (m *MainCommand) ensureCLIProviderProxy(provider *providerconfig.Service) (int, error) {
	resource := &m.providerProxy
	resource.mu.Lock()
	defer resource.mu.Unlock()
	if resource.closed {
		return 0, errors.New("Standalone provider proxy is closed")
	}
	if resource.service == nil {
		upgrader, err := migrations.New(migrations.Options{Store: m.Settings.store})
		if err != nil {
			return 0, err
		}
		user, err := settings.New(settings.Options{GitExecutable: "git", Store: m.Settings.store, ConfigDir: m.Native.ConfigHomeDirectory(), HomeDir: m.Native.Home, Environment: func() map[string]string { return m.Native.Environment }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: func(context.Context) (map[string]string, error) {
			return nil, mainCap("standalone proxy route does not consume shell environment")
		}})
		if err != nil {
			return 0, err
		}
		tls, err := tlsconfig.NewState(m.Native.readFile)
		if err != nil {
			return 0, err
		}
		proxy, err := providerproxytransport.NewStandalone(providerproxytransport.Options{Provider: provider.ProviderForProxy, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
			var object protocol.JSONObject
			value, err := user.GetUserSettings(ctx)
			if err != nil {
				return object, err
			}
			raw, err := json.Marshal(value)
			if err == nil {
				err = object.UnmarshalJSON(raw)
			}
			return object, err
		}, EnvironmentWithContext: m.cliProviderRequestEnvironment, TLS: tls, Version: m.version, NowMillis: func() int64 { return int64(m.Native.NowMillis()) }})
		if err != nil {
			return 0, err
		}
		resource.service = proxy
	}
	return resource.service.EnsurePort()
}

func (m *MainCommand) cliProviderRequestEnvironment(ctx context.Context) (map[string]string, error) {
	// Source's synchronous startup finishes before its event loop can dispatch
	// the new listener. Retain that completion boundary before owner admission.
	select {
	case <-m.providerProxy.ready:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	var environment map[string]string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		environment = map[string]string{}
		for _, property := range m.Settings.environment.Snapshot().OwnProperties() {
			var name, value string
			if json.Unmarshal(property.NameJSON, &name) == nil && json.Unmarshal(property.Value, &value) == nil {
				environment[name] = value
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	select {
	case <-receipt.Done():
		return environment, receipt.Wait()
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (m *MainCommand) closeCLIProviderProxy() {
	resource := &m.providerProxy
	resource.mu.Lock()
	resource.closed = true
	service := resource.service
	resource.mu.Unlock()
	if service != nil {
		_ = service.Close(context.Background())
	}
}
