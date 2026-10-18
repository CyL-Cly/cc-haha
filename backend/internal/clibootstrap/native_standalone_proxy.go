package clibootstrap

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type nativeStandaloneProxy struct {
	mu           sync.Mutex
	service      *providerproxytransport.Standalone
	userSettings *settings.Service
}

func (m *MainPrint) nativeEnsureStandaloneProxy(service *providerconfig.Service) (int, error) {
	if m.standaloneProxy == nil {
		m.standaloneProxy = &nativeStandaloneProxy{}
	}
	resource := m.standaloneProxy
	resource.mu.Lock()
	defer resource.mu.Unlock()
	if resource.service == nil {
		upgrader, err := migrations.New(migrations.Options{Store: m.nativeSettings.store})
		if err != nil {
			return 0, err
		}
		userSettings, err := settings.New(settings.Options{GitExecutable: "git", Store: m.nativeSettings.store, ConfigDir: m.options.Native.ConfigHomeDirectory(), HomeDir: m.options.Native.Home, Environment: func() map[string]string { return m.options.Native.Environment }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: func(context.Context) (map[string]string, error) {
			return nil, mainCap("standalone proxy route does not consume shell environment")
		}})
		if err != nil {
			return 0, err
		}
		resource.userSettings = userSettings
		proxy, err := providerproxytransport.NewStandalone(providerproxytransport.Options{Provider: service.ProviderForProxy, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
			var settings protocol.JSONObject
			value, err := userSettings.GetUserSettings(ctx)
			if err != nil {
				return settings, err
			}
			raw, err := json.Marshal(value)
			if err == nil {
				err = settings.UnmarshalJSON(raw)
			}
			return settings, err
		}, EnvironmentWithContext: func(ctx context.Context) (map[string]string, error) {
			var environment map[string]string
			receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(*cooperative.Scope) error {
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				environment = m.nativeOpenAIEnvironment()
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
		}, TLS: m.tls, Version: m.options.Version, Trace: m.options.Trace, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }})
		if err != nil {
			return 0, err
		}
		resource.service = proxy
	}
	return resource.service.EnsurePort()
}
func (m *MainPrint) closeNativeStandaloneProxy(ctx context.Context) error {
	if m.standaloneProxy == nil {
		return nil
	}
	resource := m.standaloneProxy
	resource.mu.Lock()
	service := resource.service
	resource.mu.Unlock()
	if service == nil {
		return nil
	}
	return service.Close(ctx)
}
