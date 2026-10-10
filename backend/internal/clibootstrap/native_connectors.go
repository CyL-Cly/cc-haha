package clibootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type NativeConnectorOptions struct {
	MCP            *mcpruntime.Service
	PluginOptions  *pluginoptions.Service
	Settings       *settings.Service
	ReloadSessions func(context.Context, string, *protocol.JSONObject) error
	// Runtime contains only native download/archive/process/checksum I/O.
	// A nil value chooses the actual production OS implementation.
	Runtime *connectors.Runtime
}

// Connectors binds the complete lifecycle and managed artifacts to actual
// shared settings, installation registry, secure options, peers and sessions.
func (p *NativeProcessSettings) Connectors(o NativeConnectorOptions) (*connectors.Service, error) {
	if o.MCP == nil || o.PluginOptions == nil || o.Settings == nil || o.ReloadSessions == nil {
		return nil, errors.New("native Connectors require shared MCP, options, settings and active-session reload")
	}
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	n, platform, store := p.n, p.platform, p.store
	p.mu.Unlock()
	catalog, err := connectors.DefaultCatalog()
	if err != nil {
		return nil, err
	}
	backend, err := connectors.NewPluginStore(connectors.PluginStoreOptions{ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], Store: store, Now: n.Now, UUID: uuidMain, Policy: p.Policy,
		MarketplaceAllowed: func(ctx context.Context, root string) error {
			policy, err := p.Policy(ctx)
			if err != nil {
				return err
			}
			source := protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: quoteMain("directory")}, protocol.JSONField{Name: "path", Value: quoteMain(root)})
			if !pluginregistry.PolicyFromSettings(policy).Allowed(source) {
				return errors.New("Marketplace source is blocked by organization policy")
			}
			return nil
		},
		UpdateEnabled: o.Settings.UpdateEnabledPlugin,
		Load: func(ctx context.Context) (connectors.PluginLoadResult, error) {
			result, err := p.RefreshPlugins(ctx, true)
			if err != nil {
				return connectors.PluginLoadResult{}, err
			}
			out := connectors.PluginLoadResult{Enabled: []connectors.LoadedPlugin{}, Errors: []string{}}
			for _, plugin := range result.Enabled {
				out.Enabled = append(out.Enabled, connectors.LoadedPlugin{Source: plugin.Source, Path: plugin.Path})
			}
			for _, failure := range result.Errors {
				out.Errors = append(out.Errors, failure.String("source"))
			}
			return out, nil
		},
		Reload: o.ReloadSessions, DeleteOptions: o.PluginOptions.Delete,
	})
	if err != nil {
		return nil, err
	}
	bridge, err := connectors.NewManagedBridge(n.Environment["CLAUDE_CONFIG_DIR"], catalog, backend, uuidMain)
	if err != nil {
		return nil, err
	}
	arch := nativeConnectorArch()
	if platform == "windows" {
		platform = "win32"
	}
	// The caller's running architecture chooses the same target spelling used
	// by the original catalog and pinned managed runtime artifacts.
	native := connectors.DefaultRuntime(platform, arch, n.Environment)
	if o.Runtime != nil {
		native = *o.Runtime
	}
	remote := connectors.RemoteDependencies{Probe: func(ctx context.Context, name string, config protocol.JSONObject) (connectors.RemoteProbe, error) {
		status, count, err := o.MCP.Probe(ctx, name, config)
		return connectors.RemoteProbe{Status: status, ToolCount: count}, err
	}, OAuth: o.MCP.Authenticate, Disconnect: o.MCP.Disconnect, ClearOAuth: o.MCP.ClearOAuth, LoadOptions: o.PluginOptions.Load, SaveOptions: o.PluginOptions.Save, DeleteOptions: o.PluginOptions.Delete, Environment: func(key string) (string, bool) { v, ok := n.Environment[key]; return v, ok }, PluginDataDirectory: func(ctx context.Context, id string) (string, error) { return nativePluginDataDirectory(n, ctx, id) }}
	return connectors.New(connectors.Options{Root: filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "connectors"), Platform: platform + "-" + arch, Definitions: catalog.Definitions, Bridge: bridge, Now: n.Now, UUID: uuidMain, CreateAdapter: func(def protocol.JSONObject, root string) (connectors.Adapter, error) {
		for _, recipe := range catalog.Skills {
			if recipe.ID == def.String("id") && recipe.Version == def.String("version") {
				return connectors.SkillAdapter(def, root, recipe, native.Download, uuidMain)
			}
		}
		for _, recipe := range catalog.Remote {
			if recipe.ID == def.String("id") {
				return connectors.RemoteAdapter(def, root, recipe, remote)
			}
		}
		return connectors.NativeAdapter(def, root, native, uuidMain)
	}})
}

func nativePluginDataDirectory(n *NativeBoundary, ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dirs := pluginregistry.ResolveDirectories(n.Home, n.Environment["CLAUDE_CONFIG_DIR"], false, func(key string) string { return n.Environment[key] })
	units := utf16.Encode([]rune(id))
	for i, c := range units {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			units[i] = '-'
		}
	}
	path := filepath.Join(dirs.Primary, "data", string(utf16.Decode(units)))
	if !filepath.IsAbs(path) {
		path = filepath.Join(n.CWD, path)
	}
	return path, os.MkdirAll(path, 0700)
}

func nativeConnectorArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}
