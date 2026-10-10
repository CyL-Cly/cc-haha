package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (p *NativeProcessSettings) MCPConfigurations(plugins *pluginmcp.Service, nonInteractive bool) (*mcpconfig.Service, error) {
	p.mu.Lock()
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	n := p.n
	p.mu.Unlock()
	merged := func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := p.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	// LoadedPlugin objects own the unresolved server cache. Protect those
	// borrowed objects while extraction publishes it; environment substitutions
	// remain fresh on every configuration load, as in the original producer.
	var pluginMu sync.Mutex
	load := func(ctx context.Context) (mcpconfig.Result, error) {
		pluginMu.Lock()
		defer pluginMu.Unlock()
		loaded, err := p.LoadPlugins(ctx, "", false)
		if err != nil {
			return mcpconfig.Result{}, err
		}
		objects := []protocol.JSONObject{}
		for _, plugin := range loaded.Enabled {
			data, err := json.Marshal(plugin)
			if err != nil {
				return mcpconfig.Result{}, err
			}
			var value protocol.JSONObject
			if err = value.UnmarshalJSON(data); err != nil {
				return mcpconfig.Result{}, err
			}
			objects = append(objects, value)
		}
		failures := []protocol.JSONObject{}
		servers, err := plugins.Extract(ctx, objects, &failures)
		if err != nil {
			return mcpconfig.Result{}, err
		}
		p.plugins.loader.PublishMCPCache(loaded, objects)
		return mcpconfig.Result{Servers: servers, Errors: failures}, nil
	}
	return mcpconfig.New(mcpconfig.Options{CWD: n.CWD, ManagedRoot: n.ManagedRoot, Platform: p.platform, ReadFile: n.readFile, Environment: func(key string) (string, bool) { value, ok := n.Environment[key]; return value, ok }, NonInteractive: nonInteractive, Settings: merged, Policy: p.Policy, SourceEnabled: func(source string) bool {
		for _, name := range enabledSources(p.sources.AllowedSettingSources) {
			if name == source {
				return true
			}
		}
		return false
	}, Global: func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := p.global.Get()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}, Project: func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, n.CWD)
		if key == "" {
			key = n.CWD
		}
		value, err := p.global.GetProject(strings.ReplaceAll(key, "\\", "/"))
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}, Plugins: load, Enabled: p.MCPExecutionEnabled, BypassConfirmed: func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		policy, err := p.Policy(ctx)
		if err != nil {
			return false, err
		}
		if string(policy.Get("skipDangerousModePermissionPrompt")) == "true" {
			return true, nil
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, source := range []string{"userSettings", "localSettings"} {
			value, err := p.startup.GetSettingsForSource(source)
			if err != nil {
				return false, err
			}
			if value != nil && string(value.Get("skipDangerousModePermissionPrompt")) == "true" {
				return true, nil
			}
		}
		if value, ok := p.settingsState.GetCachedSettingsForSource("flagSettings"); ok && value != nil && string(value.Get("skipDangerousModePermissionPrompt")) == "true" {
			return true, nil
		}
		return false, nil
	}})
}
