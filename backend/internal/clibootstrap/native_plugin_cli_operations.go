package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"path/filepath"
	"strings"
)

func (p *NativeProcessSettings) pluginCLIReadSettings(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
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
	if cwd == "" || scope == "managed" || scope == "flag" {
		value, err = p.startup.GetSettingsForSource(source)
	} else {
		value, err = p.settings.GetEditableSource(source, cwd)
	}
	if value == nil {
		return protocol.NewJSONObject(), err
	}
	return value.Clone(), err
}
func (p *NativeProcessSettings) pluginCLIEnabled(ctx context.Context) (map[string]bool, []string, error) {
	out := map[string]bool{}
	order := []string{}
	additional, err := p.pluginCLIAdditionalEnabled(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, property := range additional.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		if strings.Contains(id, "@") && string(property.Value) == "true" {
			out[id] = true
			order = append(order, id)
		}
	}
	for _, scope := range []string{"managed", "user", "project", "local", "flag"} {
		value, err := p.pluginCLIReadSettings(ctx, scope, "")
		if err != nil {
			return nil, nil, err
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
				if !out[id] {
					order = append(order, id)
				}
				out[id] = true
			} else if string(property.Value) == "false" {
				delete(out, id)
				for i, v := range order {
					if v == id {
						order = append(order[:i], order[i+1:]...)
						break
					}
				}
			}
		}
	}
	return out, order, nil
}
func (p *NativeProcessSettings) pluginCLIWrite(ctx context.Context, scope, cwd string, patch clisettings.Patch, jobs *pluginCLIJobs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return err
	}
	source := map[string]string{"user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope]
	result, err := p.settings.UpdateNativeEditableSource(source, cwd, patch, clisettings.NativeSourceWriterBindings{StartLocalIgnore: jobs.StartLocalIgnore})
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if result.Error != nil {
		return result.Error
	}
	p.InvalidateSettingsCache()
	return nil
}
func (p *NativeProcessSettings) pluginCLIWriteEnabled(ctx context.Context, scope, cwd, id string, value *bool, jobs *pluginCLIJobs) error {
	entries := protocol.NewJSONObject()
	patch := clisettings.Patch{Object: protocol.NewJSONObject()}
	if value == nil {
		patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("enabledPlugins"), quoteMain(id)}}
	} else {
		entries.Set(id, rawMain(*value))
	}
	patch.Object.Set("enabledPlugins", rawMain(entries))
	return p.pluginCLIWrite(ctx, scope, cwd, patch, jobs)
}
func (p *NativeProcessSettings) pluginCLIClear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.InvalidateSettingsCache()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return err
	}
	p.plugins.installed.Clear()
	p.plugins.loader.Clear()
	p.plugins.hooks.Clear()
	return nil
}
func (p *NativeProcessSettings) pluginCLIOperations(options *pluginoptions.Service, jobs *pluginCLIJobs) (*pluginsapi.Service, error) {
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	bindings, n, store := p.plugins, p.n, p.store
	p.mu.Unlock()
	tls, err := tlsconfig.NewState(n.readFile)
	if err != nil {
		return nil, err
	}
	client, err := webfetch.NewHTTPClient(n.Environment, tls)
	if err != nil {
		return nil, err
	}
	resolver, err := pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: n.Home, WorkDir: n.CWD, PluginsDir: bindings.directories.Primary, Environment: n.Environment, Store: store, Now: n.Now, Client: client})
	if err != nil {
		return nil, err
	}
	return pluginsapi.NewCoreOperations(pluginsapi.CoreOperationsOptions{WorkDir: n.CWD, PluginsDir: bindings.directories.Primary, Installed: bindings.installed, Materializer: bindings.materializer, Now: n.Now, Load: func(ctx context.Context, _ bool) (pluginregistry.Result, error) { return p.LoadPlugins(ctx, "", true) }, ReadSettings: p.pluginCLIReadSettings, WriteEnabled: func(ctx context.Context, scope, cwd, id string, value *bool) error {
		return p.pluginCLIWriteEnabled(ctx, scope, cwd, id, value, jobs)
	}, EditableEnabled: func(ctx context.Context) (map[string]bool, error) {
		out, _, err := p.pluginCLIEnabled(ctx)
		return out, err
	}, EnabledOrder: func(ctx context.Context) ([]string, error) {
		_, order, err := p.pluginCLIEnabled(ctx)
		return order, err
	}, WriteEnabledMany: func(ctx context.Context, scope string, ids []string) error {
		entries := protocol.NewJSONObject()
		for _, id := range ids {
			entries.Set(id, []byte("true"))
		}
		return p.pluginCLIWrite(ctx, scope, "", clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "enabledPlugins", Value: rawMain(entries)})}, jobs)
	}, ClearCaches: p.pluginCLIClear, KnownMarketplaces: func(ctx context.Context) (protocol.JSONObject, error) {
		return pluginsapi.ReadKnownMarketplaces(ctx, bindings.directories.Primary)
	}, LookupPlugin: resolver.Lookup, DeleteOptions: options.Delete, IsBuiltin: func(id string) bool { return strings.HasSuffix(id, "@builtin") }})
}

func (p *NativeProcessSettings) pluginCLIMarketplaces(jobs *pluginCLIJobs) (*pluginsapi.MarketplaceManager, error) {
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	bindings, n, store := p.plugins, p.n, p.store
	p.mu.Unlock()
	tls, err := tlsconfig.NewState(n.readFile)
	if err != nil {
		return nil, err
	}
	client, err := webfetch.NewHTTPClient(n.Environment, tls)
	if err != nil {
		return nil, err
	}
	resolver, err := pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: n.Home, WorkDir: n.CWD, PluginsDir: bindings.directories.Primary, Environment: n.Environment, Store: store, Now: n.Now, Client: client})
	if err != nil {
		return nil, err
	}
	return pluginsapi.NewMarketplaceManager(pluginsapi.MarketplaceManagerOptions{Resolver: resolver, Seeds: bindings.directories.Seeds, ReadSettings: p.pluginCLIReadSettings, ClearCaches: p.pluginCLIClear, WriteSettings: func(ctx context.Context, scope string, object protocol.JSONObject, paths [][]string) error {
		patch := clisettings.Patch{Object: object}
		for _, path := range paths {
			tokens := []json.RawMessage{}
			for _, token := range path {
				tokens = append(tokens, quoteMain(token))
			}
			patch.UndefinedPaths = append(patch.UndefinedPaths, tokens)
		}
		return p.pluginCLIWrite(ctx, scope, "", patch, jobs)
	}})
}

func (p *NativeProcessSettings) pluginCLIAdditionalEnabled(ctx context.Context) (protocol.JSONObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return protocol.JSONObject{}, err
	}
	out := protocol.NewJSONObject()
	for _, dir := range p.pluginAdditional {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(p.n.CWD, dir)
		}
		for _, name := range []string{"settings.json", "settings.local.json"} {
			if err := ctx.Err(); err != nil {
				return protocol.JSONObject{}, err
			}
			parsed, err := p.settings.ParseFile(filepath.Join(dir, ".claude", name))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			if parsed.Settings == nil {
				continue
			}
			var entries protocol.JSONObject
			_ = entries.UnmarshalJSON(parsed.Settings.Get("enabledPlugins"))
			for _, property := range entries.OwnProperties() {
				_ = out.SetPropertyJSON(property.NameJSON, property.Value)
			}
		}
	}
	return out, nil
}
