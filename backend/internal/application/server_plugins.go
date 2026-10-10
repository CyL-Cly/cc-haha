package application

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type desktopPluginRuntime struct {
	mu       sync.Mutex
	closed   bool
	cwd      string
	services map[string]*pluginsapi.Service
	mcp      *desktopMCPRuntime
	agents   *desktopAgentRuntime
	settings *settings.Service
	reload   func(context.Context, string) (protocol.JSONObject, error)
}

func (r *desktopPluginRuntime) get(ctx context.Context, cwd string) (*pluginsapi.Service, error) {
	// Original PluginService loads the process plugin state; request cwd only
	// selects installation metadata and the Agent definition count during reload.
	cwd = r.cwd
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, context.Canceled
	}
	if service := r.services[cwd]; service != nil {
		return service, nil
	}
	if _, err := r.mcp.get(ctx, cwd); err != nil {
		return nil, err
	}
	entryCWD := cwd
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		entryCWD = real
	}
	r.mcp.mu.Lock()
	entry, exists := r.mcp.entries[entryCWD]
	r.mcp.mu.Unlock()
	if !exists {
		return nil, context.Canceled
	}
	service, err := entry.settings.Plugins(clibootstrap.NativePluginsManagementOptions{MCP: entry.plugins, Options: entry.options, Settings: r.settings, AgentsCount: func(ctx context.Context, cwd string) (int, error) {
		definitions, err := r.agents.load(ctx, cwd)
		if err != nil {
			return 0, err
		}
		return len(definitions.All), nil
	}, ClearRuntimeCaches: r.clearCaches, ReloadSession: r.reload})
	if err != nil {
		return nil, err
	}
	if r.services == nil {
		r.services = map[string]*pluginsapi.Service{}
	}
	r.services[cwd] = service
	return service, nil
}

func (r *desktopPluginRuntime) clearCaches(ctx context.Context) error {
	r.mcp.mu.Lock()
	entries := make([]desktopMCPEntry, 0, len(r.mcp.entries))
	for _, entry := range r.mcp.entries {
		entries = append(entries, entry)
	}
	r.mcp.mu.Unlock()
	for _, entry := range entries {
		entry.settings.InvalidateSettingsCache()
		entry.options.Clear()
		entry.config.ClearCaches()
	}
	return r.agents.invalidate(ctx)
}

func (r *desktopPluginRuntime) close() {
	r.mu.Lock()
	r.closed = true
	r.services = nil
	r.mu.Unlock()
}
