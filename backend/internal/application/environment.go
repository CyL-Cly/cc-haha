package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// ResolveProcessEnvironment runs the actual saved-provider resolver before the
// native policy/transport snapshot. Each session gets a separate env value;
// concurrent sessions do not mutate the server's process environment.
func ResolveProcessEnvironment(ctx context.Context, store *storage.Store, base map[string]string, selection *providerconfig.Selection, options ...providerconfig.ManagedEnvironmentOptions) (map[string]string, error) {
	home := base["HOME"]
	if runtime.GOOS == "windows" && base["USERPROFILE"] != "" {
		home = base["USERPROFILE"]
	}
	if home == "" {
		return nil, errors.New("native session requires HOME or USERPROFILE")
	}
	config := base["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	if selection == nil && (len(options) == 0 || !options[0].ProxyReady) {
		// The ordinary CLI starts its own proxy inside the actual settings/env
		// producer. Do not freeze a nonexistent desktop/default port before that
		// producer runs. Host-owned env is also preserved for that same boundary.
		host := strings.ToLower(strings.TrimSpace(base["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"]))
		deferToStartup := host == "1" || host == "true" || host == "yes" || host == "on"
		if !deferToStartup {
			service, e := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: config})
			if e != nil {
				return nil, e
			}
			selected, e := service.ProviderForProxy(ctx, "")
			if e != nil {
				return nil, e
			}
			deferToStartup = selected != nil && (selected.APIFormat != "anthropic" || !selected.SupportsNestedToolResultMedia)
		}
		if deferToStartup {
			env := make(map[string]string, len(base)+1)
			for k, v := range base {
				env[k] = v
			}
			env["CLAUDE_CONFIG_DIR"] = config
			return env, nil
		}
	}
	env, err := providerconfig.ResolveRuntimeEnv(ctx, store, config, base, selection, options...)
	if err != nil {
		return nil, err
	}
	env["CLAUDE_CONFIG_DIR"] = config
	if selection != nil && selection.ProviderID != nil {
		env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] = "1"
	} else if selection == nil && len(options) > 0 && options[0].ProxyReady {
		service, e := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: config})
		if e != nil {
			return nil, e
		}
		index, e := service.ReadIndex(ctx)
		if e != nil {
			return nil, e
		}
		var active string
		_ = json.Unmarshal(index["activeId"], &active)
		if active != "" {
			// A desktop runtime keeps the selected provider even if another
			// conversation later changes the app's active/default provider.
			env, e = providerconfig.ResolveRuntimeEnv(ctx, store, config, base, &providerconfig.Selection{ProviderID: &active}, options...)
			if e != nil {
				return nil, e
			}
			env["CLAUDE_CONFIG_DIR"] = config
			env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] = "1"
		}
	}
	return env, nil
}
