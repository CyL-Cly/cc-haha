package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ReloadComponents runs on the actual session Owner. It replaces the current
// command snapshots in place, without starting another session or ASK.
func (m *MainPrint) ReloadComponents(ctx context.Context) (protocol.JSONObject, error) {
	var response protocol.JSONObject
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		response, err = m.reloadComponentsOnOwner(ctx, scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return response, err
}

func (m *MainPrint) reloadComponentsOnOwner(ctx context.Context, scope *cooperative.Scope) (response protocol.JSONObject, failure error) {
	if m.nativeSettings == nil || m.closed {
		return response, cooperative.ErrClosed
	}
	m.nativeSettings.InvalidateSettingsCache()
	m.invalidateComputerUseSkillGate()
	plugins, err := m.nativeSettings.RefreshPlugins(ctx, true)
	if err != nil {
		return response, err
	}
	if err = m.reinitializeNativeLSPOnOwner(scope); err != nil {
		return response, err
	}
	if err := m.workflowDefinitions.ClearCommandsOnOwner(scope); err != nil {
		return response, err
	}
	m.invalidateNativeCommandCatalog()
	definitions, metadata, err := m.nativeCommandDefinitions(ctx, scope)
	if err != nil {
		return response, err
	}
	if err = m.hook.UpdateSnapshot(); err != nil {
		return response, err
	}
	if m.mcpTools != nil {
		if err = m.mcpTools.Discover(scope); err != nil {
			return response, err
		}
	}
	m.Registry.ReplaceNativePrompts(definitions, metadata, false)
	if m.agents != nil {
		if err := m.agents.Clear(scope); err != nil {
			return response, err
		}
		current, err := m.agents.List(scope)
		if err != nil {
			return response, err
		}
		names := []string{}
		for _, definition := range current.Active {
			names = append(names, definition.Name)
		}
		m.Registry.ReplaceNativeAgents(names)
	}
	rules, err := m.nativePermissionRules()
	if err != nil {
		return response, err
	}
	if err = m.Host.ReloadNativeSettings(scope, rules); err != nil {
		return response, err
	}
	m.userContext.ClearUserContext()
	if m.memoryPaths != nil {
		m.memoryPaths.Clear()
	}
	if m.styles != nil {
		m.styles.ClearAll()
		m.styles.ClearDirectories()
	}
	encode := func(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
	servers := []protocol.JSONObject{}
	mcpErrors := 0
	if m.mcpTools != nil {
		servers, err = m.mcpTools.ServerStatuses(ctx, scope)
		if err != nil {
			return response, err
		}
		mcpErrors, err = m.mcpTools.FailureCount(scope)
		if err != nil {
			return response, err
		}
	}
	response = protocol.NewJSONObject(protocol.JSONField{Name: "commands", Value: encode(m.Registry.ControlCommands())}, protocol.JSONField{Name: "agents", Value: encode(m.Registry.Catalog().Agents)}, protocol.JSONField{Name: "plugins", Value: encode(metadata)}, protocol.JSONField{Name: "mcpServers", Value: encode(servers)}, protocol.JSONField{Name: "error_count", Value: encode(len(plugins.Errors) + mcpErrors)})
	if state := m.nativePluginInstallation; state != nil {
		state.mu.Lock()
		state.needsRefresh = false
		state.initialErrors = append([]protocol.JSONObject{}, plugins.Errors...)
		state.mu.Unlock()
		state.publish()
	}
	if m.options.OnCommandsChanged != nil {
		return response, m.options.OnCommandsChanged(m.Registry.ControlCommands())
	}
	return response, nil
}
