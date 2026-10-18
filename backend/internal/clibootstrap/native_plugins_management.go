package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type NativePluginsManagementOptions struct {
	MCP                *pluginmcp.Service
	Options            *pluginoptions.Service
	Settings           *settings.Service
	AgentsCount        func(context.Context, string) (int, error)
	ClearRuntimeCaches func(context.Context) error
	ReloadSession      func(context.Context, string) (protocol.JSONObject, error)
}

func (p *NativeProcessSettings) Plugins(o NativePluginsManagementOptions) (*pluginsapi.Service, error) {
	if o.MCP == nil || o.Options == nil || o.Settings == nil || o.AgentsCount == nil || o.ClearRuntimeCaches == nil || o.ReloadSession == nil {
		return nil, errors.New("Plugins require shared MCP/options/settings/agents/runtime/session producers")
	}
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	bindings, n, store := p.plugins, p.n, p.store
	p.mu.Unlock()
	primary := bindings.directories.Primary
	if !filepath.IsAbs(primary) {
		primary = filepath.Join(n.CWD, primary)
	}
	resolver, err := pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: n.Home, WorkDir: n.CWD, PluginsDir: primary, Environment: n.nativeEnvironmentSnapshot(), Store: store, Now: n.Now})
	if err != nil {
		return nil, err
	}
	skills, err := p.SkillCommands(NativeSkillOptions{})
	if err != nil {
		return nil, err
	}
	read := func(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		if err := p.ensureSettings(); err != nil {
			return protocol.JSONObject{}, err
		}
		source := map[string]string{"managed": "policySettings", "user": "userSettings", "project": "projectSettings", "local": "localSettings", "flag": "flagSettings"}[scope]
		var value *protocol.JSONObject
		var err error
		if scope == "managed" || scope == "flag" || cwd == "" {
			value, err = p.startup.GetSettingsForSource(source)
		} else {
			value, err = p.settings.GetEditableSource(source, cwd)
		}
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	clear := func(ctx context.Context) error {
		p.InvalidateSettingsCache()
		p.mu.Lock()
		if err := p.ensureSettings(); err != nil {
			p.mu.Unlock()
			return err
		}
		bindings.installed.Clear()
		bindings.hooks.Clear()
		p.mu.Unlock()
		if err := o.ClearRuntimeCaches(ctx); err != nil {
			return err
		}
		if bindings.hooks.Snapshot() != nil {
			result, err := p.LoadPlugins(ctx, "", false)
			if err != nil {
				return err
			}
			bindings.hooks.Prune(result.Enabled)
		}
		return nil
	}
	return pluginsapi.New(pluginsapi.Options{WorkDir: n.CWD, PluginsDir: primary, Installed: bindings.installed, Materializer: bindings.materializer, Now: n.Now, Load: func(ctx context.Context, full bool) (pluginregistry.Result, error) {
		return p.LoadPlugins(ctx, "", full)
	}, Hydrate: func(ctx context.Context, result *pluginregistry.Result) error {
		objects := []protocol.JSONObject{}
		for i := range result.Enabled {
			plugin := &result.Enabled[i]
			data, err := json.Marshal(plugin)
			if err != nil {
				return err
			}
			var fields protocol.JSONObject
			if err = fields.UnmarshalJSON(data); err != nil {
				return err
			}
			if plugin.MCPServers == nil {
				servers, err := o.MCP.LoadPluginMCPServers(ctx, fields, &result.Errors)
				if err != nil {
					return err
				}
				plugin.MCPServers = servers
				if servers != nil {
					data, err = servers.MarshalJSON()
					if err != nil {
						return err
					}
					fields.Set("mcpServers", data)
				}
			}
			if plugin.LSPServers == nil {
				servers, err := pluginsapi.LoadLSP(ctx, *plugin, &result.Errors)
				if err != nil {
					return err
				}
				plugin.LSPServers = servers
			}
			objects = append(objects, fields)
		}
		bindings.loader.PublishMCPCache(*result, objects)
		return nil
	}, ReadSettings: read, WriteEnabled: func(ctx context.Context, scope, cwd, id string, value *bool) error {
		p.mu.Lock()
		err := p.ensureSettings()
		p.mu.Unlock()
		if err != nil {
			return err
		}
		return o.Settings.UpdateEnabledPluginAtScope(ctx, id, value, scope, cwd)
	}, EditableEnabled: func(ctx context.Context) (map[string]bool, error) {
		// Source treats arrays as enabled in explicit scope decisions, but this
		// original editable-scope projection processes literal booleans only.
		out := map[string]bool{}
		for _, scope := range []string{"managed", "user", "project", "local", "flag"} {
			value, err := read(ctx, scope, "")
			if err != nil {
				return nil, err
			}
			var enabled protocol.JSONObject
			_ = enabled.UnmarshalJSON(value.Get("enabledPlugins"))
			for _, property := range enabled.OwnProperties() {
				var id string
				_ = json.Unmarshal(property.NameJSON, &id)
				if !strings.Contains(id, "@") {
					continue
				}
				if string(property.Value) == "true" {
					out[id] = true
				} else if string(property.Value) == "false" {
					delete(out, id)
				}
			}
		}
		return out, nil
	}, ClearCaches: clear, KnownMarketplaces: func(ctx context.Context) (protocol.JSONObject, error) {
		return pluginsapi.ReadKnownMarketplaces(ctx, primary)
	}, LookupPlugin: resolver.Lookup, DeleteOptions: o.Options.Delete, SkillsCount: func(ctx context.Context) (int, error) {
		result, err := p.LoadPlugins(ctx, "", false)
		if err != nil {
			return 0, err
		}
		return len(skills.PluginSkills(ctx, result.Enabled)), nil
	}, AgentsCount: o.AgentsCount, LoadHooks: p.LoadPluginHooks, ReloadSession: o.ReloadSession,
		// The external initBuiltinPlugins table currently registers none. These
		// detail lookup uses the declaration table. Source's operation selector
		// recognizes the @builtin suffix even for an undeclared ID; the shared
		// loader still exposes only actually registered definitions.
		IsBuiltin: func(id string) bool {
			return strings.HasSuffix(id, "@builtin")
		}, BuiltinDetail: func(name string) (protocol.JSONObject, error) {
			if definition, ok := bindings.builtins.Lookup(name); ok {
				out := protocol.NewJSONObject()
				if definition.Hooks != nil {
					data, _ := definition.Hooks.MarshalJSON()
					out.Set("hooks", data)
				}
				if definition.MCPServers != nil {
					data, _ := definition.MCPServers.MarshalJSON()
					out.Set("mcpServers", data)
				}
				return out, nil
			}
			return protocol.JSONObject{}, errors.New("Builtin plugin declaration not found")
		},
	})
}

func (p *NativeProcessSettings) LoadPluginHooks(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return err
	}
	result, err := p.plugins.loader.LoadCached(ctx)
	if err != nil {
		return err
	}
	p.plugins.hooks.LoadPlugins(result.Enabled)
	return nil
}
func (p *NativeProcessSettings) RegisteredPluginHooks(ctx context.Context) (*protocol.JSONObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	return p.plugins.hooks.Snapshot(), nil
}
