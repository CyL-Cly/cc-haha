package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalReloadArray(value json.RawMessage) []json.RawMessage {
	var rows []json.RawMessage
	_ = json.Unmarshal(value, &rows)
	return rows
}

func (m *MainPrint) terminalReloadMetrics(ctx context.Context, scope *cooperative.Scope) (protocol.JSONObject, error) {
	pending, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
		plugins, err := m.nativeSettings.LoadPlugins(ctx, "", false)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		resolver, _, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		mcpCount, lspCount, hookCount := 0, 0, 0
		objects := []protocol.JSONObject{}
		for _, plugin := range plugins.Enabled {
			fields := mainObj(rawMain(plugin))
			servers := plugin.MCPServers
			if servers == nil {
				servers, err = resolver.LoadPluginMCPServers(ctx, fields, &plugins.Errors)
				if err != nil {
					return protocol.JSONObject{}, err
				}
			}
			if servers != nil {
				mcpCount += len(servers.OwnProperties())
				fields.Set("mcpServers", rawMain(servers))
			}
			servers = plugin.LSPServers
			if servers == nil {
				servers, err = pluginsapi.LoadLSP(ctx, plugin, &plugins.Errors)
				if err != nil {
					return protocol.JSONObject{}, err
				}
			}
			if servers != nil {
				lspCount += len(servers.OwnProperties())
				fields.Set("lspServers", rawMain(servers))
			}
			if plugin.HooksConfig != nil {
				for _, group := range plugin.HooksConfig.OwnProperties() {
					for _, matcher := range terminalReloadArray(group.Value) {
						hookCount += len(terminalReloadArray(mainObj(matcher).Get("hooks")))
					}
				}
			}
			objects = append(objects, fields)
		}
		m.nativeSettings.mu.Lock()
		m.nativeSettings.plugins.loader.PublishComponentCache(plugins, objects)
		m.nativeSettings.mu.Unlock()
		skills := m.skillCommands.MemoizedPluginSkills(ctx, plugins.Enabled)
		return protocol.NewJSONObject(fieldMain("enabled_count", len(plugins.Enabled)), fieldMain("disabled_count", len(plugins.Disabled)), fieldMain("skill_count", len(skills)), fieldMain("hook_count", hookCount), fieldMain("mcp_count", mcpCount), fieldMain("lsp_count", lspCount), fieldMain("error_count", len(plugins.Errors))), nil
	})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value, err := pending.Await(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	agents, err := m.agents.List(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value.Set("agent_count", rawMain(len(agents.All)))
	return value, nil
}

func terminalReloadMessage(value protocol.JSONObject) string {
	message := "Reloaded: "
	for index, row := range []struct{ key, noun string }{{"enabled_count", "plugin"}, {"skill_count", "skill"}, {"agent_count", "agent"}, {"hook_count", "hook"}, {"mcp_count", "plugin MCP server"}, {"lsp_count", "plugin LSP server"}} {
		var count int
		_ = json.Unmarshal(value.Get(row.key), &count)
		if index > 0 {
			message += " · "
		}
		noun := row.noun
		if count != 1 {
			noun += "s"
		}
		message += fmt.Sprintf("%d %s", count, noun)
	}
	var errors int
	_ = json.Unmarshal(value.Get("error_count"), &errors)
	if errors > 0 {
		noun := "errors"
		if errors == 1 {
			noun = "error"
		}
		message += fmt.Sprintf("\n%d %s during load. Run /doctor for details.", errors, noun)
	}
	return message
}

// TerminalReloadPlugins includes the local command's canonical replay on the
// same serial ASK. The control-request ReloadComponents API remains unchanged.
func (m *MainPrint) TerminalReloadPlugins(ctx context.Context, args string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || !m.options.Interactive || m.agentChild {
			return cooperative.ErrRestricted
		}
		user, err := m.Host.PrepareTerminalPluginReloadOnOwner(scope, args)
		if err != nil {
			return err
		}
		result, err = m.reloadComponentsOnOwner(ctx, scope)
		if err != nil {
			return err
		}
		metrics, err := m.terminalReloadMetrics(ctx, scope)
		if err != nil {
			return err
		}
		for _, field := range metrics.OwnProperties() {
			if err = result.SetPropertyJSON(field.NameJSON, field.Value); err != nil {
				return err
			}
		}
		message := terminalReloadMessage(result)
		if err = m.Host.AppendTerminalPluginReloadOnOwner(scope, user, message); err != nil {
			return err
		}
		result.Set("message", quoteMain(message))
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
