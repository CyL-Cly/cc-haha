package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
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
	flagged      *pluginsapi.FlaggedRegistry
	roots        *pluginsapi.MarketplaceRoots
}

// This constructor owns actual file/config/installation producers. It does not
// allocate a second settings cache or execute legacy TypeScript business code.
func newNativePluginBindings(n *NativeBoundary, platform string, settings *clisettings.Service, state *clisettings.State, store *storage.Store, initial func() (*protocol.JSONObject, error), additional []string, inline []string, cowork ...bool) (*nativePluginBindings, error) {
	environment := n.nativeEnvironmentSnapshot()
	env := func(key string) string { value, _ := n.nativeEnvironmentValue(key); return value }
	useCowork := len(cowork) > 0 && cowork[0]
	config := n.ConfigHomeDirectory()
	dirs := pluginregistry.ResolveDirectories(n.Home, config, useCowork, env)
	primary := dirs.Primary
	if !filepath.IsAbs(primary) {
		primary = filepath.Join(n.CWD, primary)
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: primary, Store: store, Now: n.Now})
	if err != nil {
		return nil, err
	}
	archive, err := pluginzip.New(pluginzip.Options{Home: n.Home, TempDir: n.Temp, Environment: environment, Platform: platform, Now: n.Now})
	if err != nil {
		return nil, err
	}
	materializer, err := pluginregistry.NewMaterializer(pluginregistry.MaterializerOptions{Directories: dirs, CWD: n.CWD, Environment: environment, Archive: archive, Installed: installed, Now: n.Now})
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
		HomeDir: n.Home, ConfigDir: config, CWD: n.CWD, Environment: env, CoworkPlugins: useCowork,
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
	return &nativePluginBindings{loader: loader, archive: archive, installed: installed, materializer: materializer, directories: dirs, hooks: pluginhooks.New(), builtins: builtins, flagged: pluginsapi.NewFlaggedRegistry(primary), roots: pluginsapi.NewMarketplaceRoots()}, nil
}

// pluginReadLocked admits one real operation while p.mu still owns settings
// construction/cache selection. The caller releases the mutex before IO and
// releases this read only after the actual loader returns. Close rejects new
// admissions and joins every read before extracted archive/global cleanup.
func (p *NativeProcessSettings) pluginReadLocked() (*nativePluginBindings, error) {
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	p.pluginReads.Add(1)
	return p.plugins, nil
}

func (p *NativeProcessSettings) LoadPlugins(ctx context.Context, project string, full bool) (pluginregistry.Result, error) {
	p.mu.Lock()
	bindings, err := p.pluginReadLocked()
	if err != nil {
		p.mu.Unlock()
		return pluginregistry.Result{}, err
	}
	var producer *pluginregistry.LoadFuture
	if project == "" {
		if full {
			producer = bindings.loader.FullProducer()
		} else {
			producer = bindings.loader.CachedProducer()
		}
	}
	p.mu.Unlock()
	defer p.pluginReads.Done()
	if project != "" {
		return bindings.loader.LoadProject(ctx, project)
	}
	return producer.Load(ctx)
}

func (p *NativeProcessSettings) EnabledForSkills(ctx context.Context) ([]skillregistry.Plugin, error) {
	p.mu.Lock()
	bindings, err := p.pluginReadLocked()
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	// Preserve the existing synchronous Clear/installed/selection prefix;
	// a different admission cannot select the old cached receipt between them.
	bindings.loader.Clear()
	bindings.installed.Clear()
	producer := bindings.loader.CachedProducer()
	p.mu.Unlock()
	defer p.pluginReads.Done()
	result, err := producer.Load(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range result.Errors {
		if issue.String("type") == "plugin-cache-miss" {
			p.mu.Lock()
			full := bindings.loader.FullProducer()
			p.mu.Unlock()
			result, err = full.Load(ctx)
			break
		}
	}
	if err != nil {
		return nil, err
	}
	plugins := []skillregistry.Plugin{}
	for _, plugin := range result.Enabled {
		plugins = append(plugins, skillregistry.Plugin{Name: plugin.Name, SkillsPath: plugin.SkillsPath, SkillsPaths: plugin.SkillsPaths})
	}
	return plugins, nil
}

// Project mentions reload the same installed snapshot without materializing a
// package or changing the process plugin settings base. Admitted native reads
// retain their original loader even when a later request clears its cache.
func (p *NativeProcessSettings) RefreshInstalledPluginsForProject(ctx context.Context, project string) (pluginregistry.Result, error) {
	p.mu.Lock()
	bindings, err := p.pluginReadLocked()
	if err != nil {
		p.mu.Unlock()
		return pluginregistry.Result{}, err
	}
	bindings.installed.Clear()
	bindings.loader.Clear()
	p.mu.Unlock()
	defer p.pluginReads.Done()
	return bindings.loader.LoadProject(ctx, project)
}

// RefreshPlugins keeps clear/producer sampling together under the settings
// admission lock, then lets unrelated actual sources begin while IO is pending.
func (p *NativeProcessSettings) RefreshPlugins(ctx context.Context, full bool) (pluginregistry.Result, error) {
	p.mu.Lock()
	bindings, err := p.pluginReadLocked()
	if err != nil {
		p.mu.Unlock()
		return pluginregistry.Result{}, err
	}
	p.settingsState.ResetSettingsCache()
	bindings.installed.Clear()
	bindings.loader.Clear()
	var producer *pluginregistry.LoadFuture
	if full {
		producer = bindings.loader.FullProducer()
	} else {
		producer = bindings.loader.CachedProducer()
	}
	p.mu.Unlock()
	defer p.pluginReads.Done()
	return producer.Load(ctx)
}

// This bridge is private to the already-bound native factory. A foreign or
// retired settings object cannot borrow the real loader/archive lifetime.
func (p *NativeProcessSettings) loadNativePluginProducer(ctx context.Context, producer *pluginregistry.LoadFuture) (pluginregistry.Result, error) {
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return pluginregistry.Result{}, err
	}
	if !producer.BelongsTo(p.plugins.loader) || !p.plugins.loader.SharesSettingsState(p.settingsState) {
		p.mu.Unlock()
		return pluginregistry.Result{}, mainCap("native plugin producer settings provenance")
	}
	p.pluginReads.Add(1)
	p.mu.Unlock()
	defer p.pluginReads.Done()
	return producer.Load(ctx)
}
