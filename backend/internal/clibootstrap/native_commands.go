package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativePluginHooks() ([]protocol.JSONObject, error) {
	plugins, err := m.nativeSettings.LoadPlugins(context.Background(), "", false)
	if err != nil {
		return nil, err
	}
	hooks := []protocol.JSONObject{}
	for _, plugin := range plugins.Enabled {
		if plugin.HooksConfig != nil {
			hooks = append(hooks, plugin.HooksConfig.Clone())
		}
	}
	return hooks, nil
}

func (m *MainPrint) nativeCommandDefinitions(ctx context.Context, scopes ...*cooperative.Scope) ([]skillcommands.Definition, []json.RawMessage, error) {
	plugins, err := m.nativeSettings.LoadPlugins(ctx, "", false)
	if err != nil {
		return nil, nil, err
	}
	definitions, err := m.skillCommands.Local(ctx, m.options.Native.CWD)
	if err != nil {
		return nil, nil, err
	}
	workflowDefinitions, e := m.nativeWorkflowCommandDefinitions(ctx, scopes...)
	if e != nil {
		return nil, nil, e
	}
	definitions = append(definitions, workflowDefinitions...)
	definitions = append(definitions, m.skillCommands.PluginCommands(ctx, plugins.Enabled)...)
	definitions = append(definitions, m.skillCommands.PluginSkills(ctx, plugins.Enabled)...)
	bundled, err := m.nativeBundledSkillDefinitions()
	if err != nil {
		return nil, nil, err
	}
	if definition, enabled, e := m.nativeComputerUseSkill(); e != nil {
		return nil, nil, e
	} else if enabled {
		bundled = append(bundled, definition)
	}
	definitions = append(bundled, definitions...)
	seen := map[string]bool{}
	for _, definition := range definitions {
		seen[definition.Name()] = true
	}
	for _, definition := range m.skillCommands.Dynamic() {
		if !seen[definition.Name()] {
			definitions = append(definitions, definition)
			seen[definition.Name()] = true
		}
	}
	builtins, err := m.nativeBuiltinPromptCommands()
	if err != nil {
		return nil, nil, err
	}
	definitions = append(definitions, builtins...)
	metadata := []json.RawMessage{}
	for _, plugin := range plugins.Enabled {
		value, err := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(plugin.Name)}, protocol.JSONField{Name: "path", Value: quoteMain(plugin.Path)}, protocol.JSONField{Name: "source", Value: quoteMain(plugin.Source)}).MarshalJSON()
		if err != nil {
			return nil, nil, err
		}
		metadata = append(metadata, value)
	}
	return definitions, metadata, nil
}
