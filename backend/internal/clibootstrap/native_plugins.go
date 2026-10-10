package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type nativePluginBindings struct {
	loader       *pluginregistry.Loader
	archive      *pluginzip.Service
	installed    *pluginregistry.InstalledRegistry
	materializer *pluginregistry.Materializer
	directories  pluginregistry.Directories
	hooks        *pluginhooks.Registry
	builtins     *pluginregistry.BuiltinRegistry
}

// This constructor owns actual file/config/installation producers. It does not
// allocate a second settings cache or execute legacy TypeScript business code.
func newNativePluginBindings(n *NativeBoundary, platform string, settings *clisettings.Service, state *clisettings.State, store *storage.Store, initial func() (*protocol.JSONObject, error), additional []string, inline []string, cowork ...bool) (*nativePluginBindings, error) {
	env := func(key string) string { return n.Environment[key] }
	useCowork := len(cowork) > 0 && cowork[0]
	dirs := pluginregistry.ResolveDirectories(n.Home, n.Environment["CLAUDE_CONFIG_DIR"], useCowork, env)
	primary := dirs.Primary
	if !filepath.IsAbs(primary) {
		primary = filepath.Join(n.CWD, primary)
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: primary, Store: store, Now: n.Now})
	if err != nil {
		return nil, err
	}
	archive, err := pluginzip.New(pluginzip.Options{Home: n.Home, TempDir: n.Temp, Environment: n.Environment, Platform: platform, Now: n.Now})
	if err != nil {
		return nil, err
	}
	materializer, err := pluginregistry.NewMaterializer(pluginregistry.MaterializerOptions{Directories: dirs, CWD: n.CWD, Environment: n.Environment, Archive: archive, Installed: installed, Now: n.Now})
	if err != nil {
		return nil, err
	}
	policy := func(context.Context) (protocol.JSONObject, error) {
		value, err := n.ResolvePolicy()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	builtins := pluginregistry.NewBuiltinRegistry()
	loader, err := pluginregistry.New(pluginregistry.Options{
		HomeDir: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], CWD: n.CWD, Environment: env, CoworkPlugins: useCowork,
		// Source initBuiltinPlugins currently declares no built-in plugins.
		Builtins: builtins, Installed: installed, SettingsState: state,
		InlinePaths: inline, Prepare: materializer.Prepare, ExtractZip: archive.ExtractPlugin,
		Policy: policy,
		Settings: func(ctx context.Context, project string) (protocol.JSONObject, error) {
			if err := ctx.Err(); err != nil {
				return protocol.JSONObject{}, err
			}
			if project == "" {
				value, err := initial()
				if value == nil {
					return protocol.NewJSONObject(), err
				}
				return value.Clone(), err
			}
			enabled := protocol.NewJSONObject()
			for _, source := range []string{"userSettings", "projectSettings", "localSettings", "policySettings"} {
				var value *protocol.JSONObject
				var err error
				if source == "policySettings" {
					value, err = n.ResolvePolicy()
				} else {
					value, err = settings.GetEditableSource(source, project)
				}
				if err != nil {
					return protocol.JSONObject{}, err
				}
				if value == nil {
					continue
				}
				var plugins protocol.JSONObject
				if raw := value.Get("enabledPlugins"); raw != nil {
					_ = plugins.UnmarshalJSON(raw)
				}
				for _, property := range plugins.OwnProperties() {
					_ = enabled.SetPropertyJSON(property.NameJSON, property.Value)
				}
			}
			raw, err := enabled.MarshalJSON()
			return protocol.NewJSONObject(protocol.JSONField{Name: "enabledPlugins", Value: raw}), err
		},
		AddDirEnabled: func(ctx context.Context) (protocol.JSONObject, error) {
			enabled := protocol.NewJSONObject()
			for _, dir := range additional {
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(n.CWD, dir)
				}
				for _, file := range []string{"settings.json", "settings.local.json"} {
					if err := ctx.Err(); err != nil {
						return protocol.JSONObject{}, err
					}
					parsed, err := settings.ParseFile(filepath.Join(dir, ".claude", file))
					if err != nil {
						return protocol.JSONObject{}, err
					}
					if parsed.Settings == nil {
						continue
					}
					var plugins protocol.JSONObject
					if raw := parsed.Settings.Get("enabledPlugins"); raw != nil {
						_ = plugins.UnmarshalJSON(raw)
					}
					for _, property := range plugins.OwnProperties() {
						_ = enabled.SetPropertyJSON(property.NameJSON, property.Value)
					}
				}
			}
			return enabled, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &nativePluginBindings{loader: loader, archive: archive, installed: installed, materializer: materializer, directories: dirs, hooks: pluginhooks.New(), builtins: builtins}, nil
}

// LoadPlugins and EnabledForSkills share the same settings lifetime as the
// server consumers. The process mutex serializes reload against close and
// prevents a caller from using extracted archives after cleanup.
func (p *NativeProcessSettings) LoadPlugins(ctx context.Context, project string, full bool) (pluginregistry.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return pluginregistry.Result{}, err
	}
	if project != "" {
		return p.plugins.loader.LoadProject(ctx, project)
	}
	if full {
		return p.plugins.loader.LoadFull(ctx)
	}
	return p.plugins.loader.LoadCached(ctx)
}

func (p *NativeProcessSettings) EnabledForSkills(ctx context.Context) ([]skillregistry.Plugin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	return p.plugins.loader.EnabledForSkills(ctx)
}

// Project mentions reload the installed snapshot without materializing a
// package or changing the process plugin settings base, as the Source caller
// does before loadInstalledPluginsForProject.
func (p *NativeProcessSettings) RefreshInstalledPluginsForProject(ctx context.Context, project string) (pluginregistry.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return pluginregistry.Result{}, err
	}
	p.plugins.installed.Clear()
	p.plugins.loader.Clear()
	return p.plugins.loader.LoadProject(ctx, project)
}

// RefreshPlugins reloads the actual installation snapshot after managed
// marketplace/cache writes. Keeping the installation registry cached here
// would leave a connector invisible until the whole server restarted.
func (p *NativeProcessSettings) RefreshPlugins(ctx context.Context, full bool) (pluginregistry.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return pluginregistry.Result{}, err
	}
	p.settingsState.ResetSettingsCache()
	p.plugins.installed.Clear()
	p.plugins.loader.Clear()
	if full {
		return p.plugins.loader.LoadFull(ctx)
	}
	return p.plugins.loader.LoadCached(ctx)
}
