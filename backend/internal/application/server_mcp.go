package application

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

type desktopMCPEntry struct {
	service  *mcpapi.Service
	settings *clibootstrap.NativeProcessSettings
	runtime  *mcpruntime.Service
	config   *mcpconfig.Service
	plugins  *pluginmcp.Service
	options  *pluginoptions.Service
}
type desktopMCPRuntime struct {
	mu           sync.Mutex
	entries      map[string]desktopMCPEntry
	closed       bool
	lifetime     context.Context
	cancel       context.CancelFunc
	owner        *cooperative.Owner
	claudeAI     *clibootstrap.CLIClaudeAIMCP
	closeDone    chan struct{}
	closeErr     error
	environment  map[string]string
	cwd, version string
	factory      NativeFactory
	settings     *clibootstrap.NativeProcessSettings
	secure       *securestorage.Store
	enableProbes mcpapi.EnableQueue
	shell        *terminalEnvironment
	syncToggle   func(context.Context, string, string, protocol.JSONObject, bool) (protocol.JSONObject, error)
}

func (r *desktopMCPRuntime) get(ctx context.Context, cwd string) (*mcpapi.Service, error) {
	if cwd == "" {
		cwd = r.cwd
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(r.cwd, cwd)
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if entry, ok := r.entries[cwd]; ok {
		return entry.service, nil
	}
	platform := runtime.GOOS
	var n *clibootstrap.NativeBoundary
	var err error
	if r.factory != nil {
		n, err = r.factory(r.environment, cwd, platform)
	} else {
		n, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: r.environment, CWD: cwd, Platform: platform})
	}
	if err != nil {
		return nil, err
	}
	settings, err := r.settings.ScopedSettings(n)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = settings.Close(context.Background())
		}
	}()
	if platform == "windows" {
		platform = "win32"
	}
	if r.secure == nil {
		r.secure, err = securestorage.New(securestorage.Options{Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], ConfigDirExplicit: n.ConfigDirExplicit, Platform: platform, Environment: n.Environment, RunSecurity: n.RunSecurity})
		if err != nil {
			return nil, err
		}
	}
	secure := r.secure
	if r.claudeAI == nil {
		if r.lifetime == nil {
			r.lifetime, r.cancel = context.WithCancel(context.Background())
		}
		if r.owner == nil {
			r.owner = cooperative.New()
		}
		r.claudeAI, err = r.settings.NewClaudeAIMCP(r.lifetime, secure, r.owner)
		if err != nil {
			return nil, err
		}
	}
	claudeAI := r.claudeAI
	pluginOptions, err := settings.PluginOptions(secure)
	if err != nil {
		return nil, err
	}
	plugins, _, err := settings.PluginMCP(pluginOptions, secure)
	if err != nil {
		return nil, err
	}
	configs, err := settings.MCPConfigurations(plugins, false)
	if err != nil {
		return nil, err
	}
	editor, err := settings.MCPEditor(configs)
	if err != nil {
		return nil, err
	}
	auth, err := mcpauth.New(mcpauth.Options{Secure: secure, Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], CWD: cwd, Platform: platform, Environment: n.Environment, Settings: func(context.Context) (protocol.JSONObject, error) {
		v, err := settings.InitialSettings()
		if v == nil {
			return protocol.NewJSONObject(), err
		}
		return v.Clone(), err
	}})
	if err != nil {
		return nil, err
	}
	peers, err := mcpruntime.New(mcpruntime.Options{CWD: cwd, PrepareHTTP: claudeAI.PrepareHTTP, Version: r.version, Environment: n.Environment, Enabled: settings.MCPExecutionEnabled, Authenticate: auth.Authenticate, ClearOAuth: auth.Clear, Auth: func(ctx context.Context, name string, cfg protocol.JSONObject) (mcpclient.AuthProvider, error) {
		if cfg.String("type") == "http" || cfg.String("type") == "sse" {
			return auth.Provider(ctx, name, cfg)
		}
		return nil, nil
	}})
	if err != nil {
		return nil, err
	}
	service, err := mcpapi.New(mcpapi.Options{CWD: cwd, All: func(ctx context.Context) (mcpconfig.Result, error) { return claudeAI.All(ctx, configs) }, Configurations: configs, Editor: editor, Runtime: peers, EnableProbes: &r.enableProbes, ForCWD: r.get, Preflight: func(ctx context.Context, config protocol.JSONObject) error {
		return inspectMCPHostCommand(ctx, cwd, n.Environment, config, r.shell.Get)
	}, SyncToggle: func(ctx context.Context, session, name string, config protocol.JSONObject, enabled bool) (protocol.JSONObject, error) {
		if r.syncToggle == nil {
			return protocol.JSONObject{}, errors.New("missing MCP active-session sync")
		}
		identity := config.Clone()
		identity.Set("projectKey", raw(editor.ProjectKey()))
		path, err := editor.ProjectPath(name, config.String("scope"))
		if err != nil {
			return protocol.JSONObject{}, err
		}
		identity.Set("projectPath", raw(path))
		return r.syncToggle(ctx, session, name, identity, enabled)
	}})
	if err != nil {
		_ = peers.Close(context.Background())
		return nil, err
	}
	if r.entries == nil {
		r.entries = map[string]desktopMCPEntry{}
	}
	r.entries[cwd] = desktopMCPEntry{service: service, settings: settings, runtime: peers, config: configs, plugins: plugins, options: pluginOptions}
	keep = true
	return service, nil
}

// close owns one finalizer. A caller deadline controls only its wait; the
// catalog/peers remain owned until all physical I/O and credential jobs join.
func (r *desktopMCPRuntime) close(ctx context.Context) error {
	r.mu.Lock()
	if r.closeDone == nil {
		r.closed = true
		r.closeDone = make(chan struct{})
		entries := r.entries
		r.entries = nil
		catalog, owner, cancel := r.claudeAI, r.owner, r.cancel
		go func() {
			if cancel != nil {
				cancel()
			}
			failures := []error{}
			for _, entry := range entries {
				failures = append(failures, entry.runtime.Close(context.Background()))
			}
			if catalog != nil {
				catalog.Close()
			}
			if owner != nil {
				owner.Close()
			}
			for _, entry := range entries {
				failures = append(failures, entry.settings.Close(context.Background()))
			}
			r.mu.Lock()
			r.closeErr = errors.Join(failures...)
			close(r.closeDone)
			r.mu.Unlock()
		}()
	}
	done := r.closeDone
	r.mu.Unlock()
	select {
	case <-done:
		r.mu.Lock()
		err := r.closeErr
		r.mu.Unlock()
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
