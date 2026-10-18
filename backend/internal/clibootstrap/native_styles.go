package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

// NativeProcessSettings owns the server process's complete CLI settings policy.
// Native files, managed policy and Source caches are shared by its consumers;
// listing styles never creates an Agent or initializes model credentials.
type NativeProcessSettings struct {
	diagnostics         *diagnostics.Process
	mu                  sync.Mutex
	pluginReads         sync.WaitGroup
	n                   *NativeBoundary
	platform            string
	sources             State
	environment         *clistartupenv.Environment
	global              *cliglobalconfig.Service
	borrowedGlobal      bool
	startup             *clistartupenv.Service
	settingsState       *clisettings.State
	settings            *clisettings.Service
	store               *storage.Store
	plugins             *nativePluginBindings
	pluginInline        []string
	pluginAdditional    []string
	pluginCowork        bool
	styles              []*systemprompt.StyleLoader
	skills              []*skillcommands.Service
	pluginOptions       []*pluginoptions.Service
	closed              bool
	agentModelResolver  NativeAgentMainLoopModelResolver
	mergeCcHahaEnv      func(protocol.JSONObject) (protocol.JSONObject, error)
	prepareNativePolicy func() error
	shells              *windowshell.Resolver
}

func NewNativeProcessSettings(n *NativeBoundary, platform string, sources State, stores ...*storage.Store) (*NativeProcessSettings, error) {
	return newNativeProcessSettings(n, platform, sources, nil, stores...)
}

// Server consumers borrow the one global writer and its cached pointer. The
// settings owner retains shutdown responsibility, including pending writes.
func (p *NativeProcessSettings) SharedGlobalConfig() (*cliglobalconfig.Service, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, mainCap("closed server settings context")
	}
	return p.global, nil
}

// ScopedSettings keeps the server's original global config writer/cache while
// binding project settings and MCP files to the addressed project.
func (p *NativeProcessSettings) ScopedSettings(n *NativeBoundary) (*NativeProcessSettings, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || n == nil || n.Home != p.n.Home || n.Environment["CLAUDE_CONFIG_DIR"] != p.n.Environment["CLAUDE_CONFIG_DIR"] {
		return nil, mainCap("shared MCP settings provenance")
	}
	scoped, err := newNativeProcessSettings(n, p.platform, p.sources, p.global, p.store)
	if err == nil {
		scoped.agentModelResolver = p.agentModelResolver
		scoped.diagnostics = p.diagnostics
		scoped.mergeCcHahaEnv = p.mergeCcHahaEnv
	}
	return scoped, err
}

func newNativeProcessSettings(n *NativeBoundary, platform string, sources State, shared *cliglobalconfig.Service, stores ...*storage.Store) (*NativeProcessSettings, error) {
	if n == nil || !n.NativeProcess || n.ResolvePolicy == nil {
		return nil, mainCap("native server settings process bindings")
	}
	if len(stores) > 1 || len(stores) == 1 && stores[0] == nil {
		return nil, mainCap("one shared server settings store")
	}
	store := storage.New(storage.Options{})
	if len(stores) == 1 {
		store = stores[0]
	}
	env := protocol.NewJSONObject()
	for k, v := range n.Environment {
		env.Set(k, quoteMain(v))
	}
	environment, err := clistartupenv.NewEnvironment(env)
	if err != nil {
		return nil, err
	}
	config := n.ConfigHomeDirectory()
	global := shared
	if global == nil {
		global, err = cliglobalconfig.New(cliglobalconfig.Options{Home: n.Home, ConfigDir: originalGlobalConfigDirectory(n, config), Now: n.Now, NowMillis: n.NowMillis, PID: os.Getpid(), FS: mainGlobalFiles{n: n}})
		if err != nil {
			return nil, err
		}
		// Source startServer enables config before serving any routes.
		if err = global.Enable(); err != nil {
			_ = global.Close()
			return nil, err
		}
	}
	return &NativeProcessSettings{n: n, platform: platform, sources: sources, environment: environment, global: global, borrowedGlobal: shared != nil, store: store}, nil
}

func (p *NativeProcessSettings) ensureSettings() error {
	if p.closed {
		return mainCap("closed server settings context")
	}
	if p.startup == nil {
		n := p.n
		config := n.ConfigHomeDirectory()
		state, err := clisettings.NewState(n.NowMillis)
		if err != nil {
			return err
		}
		settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: n.CWD, Profile: clisettings.NativeProcessProfile, Now: n.NowMillis, PID: os.Getpid(), FS: mainSettingsFiles{n: n}, NativeUserSettingsPath: func() (string, error) { return n.nativeUserSettingsPath(n.CWD, p.environment) }})
		if err != nil {
			return err
		}
		plugins, err := newNativePluginBindings(n, p.platform, settings, state, p.store, func() (*protocol.JSONObject, error) { return p.startup.GetInitialSettings() }, p.pluginAdditional, p.pluginInline, p.pluginCowork)
		if err != nil {
			return err
		}
		p.startup, err = clistartupenv.New(clistartupenv.Options{Diagnostics: p.diagnostics, Global: p.global, Settings: settings, SettingsState: state, Environment: p.environment, ConfigHome: config, FS: mainSettingsFiles{n: n}, NowMillis: n.NowMillis, Plugins: plugins.loader, MergeCcHahaEnvironment: p.mergeCcHahaEnv, NativeCcHahaConfigDirectory: p.nativeCcHahaConfigDirectory, Host: clistartupenv.Host{Profile: clistartupenv.ResolvedPolicyReleaseProfile, ResolvePolicy: n.ResolvePolicy, PrepareNativePolicy: p.prepareNativePolicy, AllowedSources: p.sources.AllowedSettingSources, FlagPath: p.sources.FlagSettingsPath}})
		if err != nil {
			return err
		}
		p.settingsState = state
		p.settings = settings
		p.plugins = plugins
	}
	return nil
}

func (p *NativeProcessSettings) initialSettings() (*protocol.JSONObject, error) {
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	if _, err := p.plugins.loader.LoadCached(context.Background()); err != nil {
		return nil, err
	}
	return p.startup.GetInitialSettings()
}

// Server settings writes reset the same Source parsed/source/session caches
// that the complete policy consumers read on their next operation.
func (p *NativeProcessSettings) InvalidateSettingsCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.settingsState != nil {
		p.settingsState.ResetSettingsCache()
		p.plugins.loader.Clear()
	}
	for _, styles := range p.styles {
		styles.ClearAll()
		styles.ClearDirectories()
	}
	for _, skills := range p.skills {
		skills.Clear()
	}
	for _, options := range p.pluginOptions {
		options.Clear()
	}
}

func (p *NativeProcessSettings) InitialSettings() (*protocol.JSONObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value, err := p.initialSettings()
	if err != nil || value == nil {
		return value, err
	}
	copy := value.Clone()
	return &copy, nil
}

func (p *NativeProcessSettings) StyleLoader() (*systemprompt.StyleLoader, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, mainCap("closed server settings context")
	}
	var style *systemprompt.StyleLoader
	var err error
	style, err = nativeStyleLoader(p.n, p.environment, p.platform, p.sources, p.InitialSettings, func(ctx context.Context) ([]systemprompt.StyleConfig, error) {
		result, err := p.LoadPlugins(ctx, "", false)
		if err != nil {
			return nil, err
		}
		return style.PluginStyles(ctx, result.Enabled)
	})
	if err == nil {
		p.styles = append(p.styles, style)
	}
	return style, err
}

func (p *NativeProcessSettings) RespectGitignore(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	settings, err := p.initialSettings()
	if err != nil {
		return false, err
	}
	if value := settings.Get("respectGitignore"); value != nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nativeSettingsTruthy(value), nil
	}
	global, err := p.global.Get()
	if err != nil {
		return false, err
	}
	if value := global.Get("respectGitignore"); value != nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nativeSettingsTruthy(value), nil
	}
	return true, nil
}

// PersistSession shares the original merged settings and checks JavaScript's
// strict numeric zero; an absent field retains Source's persistence default.
func (p *NativeProcessSettings) PersistSession(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	settings, err := p.initialSettings()
	if err != nil {
		return false, err
	}
	if settings == nil {
		return true, nil
	}
	value := bytes.TrimSpace(settings.Get("cleanupPeriodDays"))
	if len(value) == 0 || value[0] == '"' || bytes.Equal(value, []byte("null")) {
		return true, nil
	}
	number, err := strconv.ParseFloat(string(value), 64)
	return err != nil || number != 0, nil
}

func nativeSettingsTruthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	if value[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) != 0
	}
	if number, err := strconv.ParseFloat(string(value), 64); err == nil {
		return number != 0
	}
	return true
}

func (p *NativeProcessSettings) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	// Admission is closed before Wait, so no new read can Add while cleanup
	// joins actual IO. Keep p.mu available to admitted loader callbacks and to
	// callers that must observe closed rather than wait behind a held read.
	p.pluginReads.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	var archiveError error
	if p.plugins != nil {
		archiveError = p.plugins.archive.Cleanup(ctx)
	}
	if !p.borrowedGlobal {
		return errors.Join(archiveError, p.global.Close())
	}
	return archiveError
}
