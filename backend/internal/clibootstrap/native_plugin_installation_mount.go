package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The REPL's useManagePlugins mount is separate from trust-gated marketplace
// reconciliation. Full source I/O must not retain p.mu: the already-ready
// cache-only consumer and the current ASK can continue while it is pending.
func (m *MainPrint) runNativeInteractivePluginMount(ctx context.Context, state *nativePluginInstallation) error {
	p := m.nativeSettings
	plugins, err := p.plugins.loader.LoadFull(ctx)
	if err != nil {
		return err
	}
	jobs, err := newPluginCLIJobs(ctx, m.options.Native)
	if err != nil {
		return err
	}
	defer jobs.Join()
	manager, err := p.pluginCLIMarketplaces(jobs)
	if err != nil {
		return err
	}
	operations, err := p.pluginCLIOperations(m.pluginOptions, jobs)
	if err != nil {
		return err
	}
	if _, err = manager.EnforceDelisting(ctx, operations, m.options.Native.Now()); err != nil {
		return err
	}
	flagged := manager.FlaggedPlugins()
	ids := []string{}
	for _, property := range flagged.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		ids = append(ids, id)
	}
	mcp, _, err := p.PluginMCP(m.pluginOptions, m.secure)
	if err != nil {
		return err
	}
	objects := []protocol.JSONObject{}
	for _, plugin := range plugins.Enabled {
		fields := mainObj(rawMain(plugin))
		if plugin.MCPServers == nil {
			servers, err := mcp.LoadPluginMCPServers(ctx, fields, &plugins.Errors)
			if err != nil {
				return err
			}
			if servers != nil {
				fields.Set("mcpServers", rawMain(servers))
			}
		}
		if plugin.LSPServers == nil {
			servers, err := pluginsapi.LoadLSP(ctx, plugin, &plugins.Errors)
			if err != nil {
				return err
			}
			if servers != nil {
				fields.Set("lspServers", rawMain(servers))
			}
		}
		objects = append(objects, fields)
	}
	p.plugins.loader.PublishComponentCache(plugins, objects)
	if err := ctx.Err(); err != nil {
		return err
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(_ *cooperative.Scope) error {
		if m.closed || m.shuttingDown {
			return cooperative.ErrClosed
		}
		// setup/getCommands and SessionStart already consumed the command and
		// hook memos. Initial mount does not clear or replace those snapshots;
		// /reload-plugins and a new marketplace install own actual activation.
		state.mu.Lock()
		state.flaggedPlugins = ids
		state.initialErrors = append([]protocol.JSONObject{}, plugins.Errors...)
		state.mu.Unlock()
		state.publish()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
