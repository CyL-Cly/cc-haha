package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpb"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalPluginSelection is the actual LoadedPlugin captured by ManagePlugins
// when a user selects an installed row. Its manifest survives update/uninstall,
// just like the original component's selectedPlugin reference.
type TerminalPluginSelection struct {
	owner  *MainPrint
	id     string
	plugin pluginregistry.Loaded
}

func (m *MainPrint) CaptureTerminalPluginSelection(ctx context.Context, id string) (*TerminalPluginSelection, error) {
	if m == nil || !m.options.Interactive || m.options.AgentID != "" {
		return nil, cooperative.ErrRestricted
	}
	value, err := m.terminalPluginWorker(ctx, func(worker context.Context, _ *pluginCLIJobs) (protocol.JSONObject, error) {
		loaded, err := m.nativeSettings.LoadPlugins(worker, "", true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, plugin := range append(loaded.Enabled, loaded.Disabled...) {
			if plugin.Source == id || plugin.Repository == id {
				return mainObj(rawMain(plugin)), nil
			}
		}
		return protocol.JSONObject{}, fmt.Errorf("Plugin %q is no longer loaded", id)
	})
	if err != nil {
		return nil, err
	}
	var plugin pluginregistry.Loaded
	if err = json.Unmarshal(rawMain(value), &plugin); err != nil {
		return nil, err
	}
	return &TerminalPluginSelection{owner: m, id: id, plugin: plugin}, nil
}

func (m *MainPrint) validTerminalPluginSelection(selected *TerminalPluginSelection) bool {
	return m != nil && selected != nil && selected.owner == m && m.options.Interactive && m.options.AgentID == "" && selected.id != "" && (selected.plugin.Source == selected.id || selected.plugin.Repository == selected.id)
}

func (m *MainPrint) TerminalSelectedPluginConfiguration(ctx context.Context, selected *TerminalPluginSelection, all bool) (protocol.JSONObject, error) {
	if !m.validTerminalPluginSelection(selected) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return m.terminalPluginWorker(ctx, func(worker context.Context, _ *pluginCLIJobs) (protocol.JSONObject, error) {
		return m.terminalSelectedPluginConfiguration(worker, selected, all)
	})
}

func (m *MainPrint) terminalSelectedPluginConfiguration(ctx context.Context, selected *TerminalPluginSelection, all bool) (protocol.JSONObject, error) {
	resolver, _, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	steps := []protocol.JSONObject{}
	add := func(server, title string, schema, values protocol.JSONObject) error {
		if len(schema.OwnProperties()) == 0 {
			return nil
		}
		if valid, _ := mcpb.ValidateUserConfig(values, schema); valid && !all {
			return nil
		}
		if !all && server == "" {
			missing := protocol.NewJSONObject()
			for _, property := range schema.OwnProperties() {
				field := protocol.NewJSONObject()
				if err := field.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
					return err
				}
				if valid, _ := mcpb.ValidateUserConfig(values, field); !valid {
					_ = missing.SetPropertyJSON(property.NameJSON, property.Value)
				}
			}
			schema = missing
		}
		visible, present := values.Clone(), protocol.NewJSONObject()
		for _, property := range schema.OwnProperties() {
			if string(mainObj(property.Value).Get("sensitive")) != "true" {
				continue
			}
			value, err := values.GetPropertyJSON(property.NameJSON)
			if err != nil {
				return err
			}
			if value != nil {
				_ = present.SetPropertyJSON(property.NameJSON, []byte("true"))
			}
			_ = visible.DeletePropertyJSON(property.NameJSON)
		}
		steps = append(steps, protocol.NewJSONObject(fieldMain("server", server), fieldMain("title", title), fieldMain("schema", schema), fieldMain("values", visible), fieldMain("savedSensitive", present)))
		return nil
	}
	values, err := m.pluginOptions.Load(ctx, selected.id)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err = add("", selected.plugin.Name, mainObj(selected.plugin.Manifest.Get("userConfig")), values); err != nil {
		return protocol.JSONObject{}, err
	}
	if !all {
		var channels []protocol.JSONObject
		_ = json.Unmarshal(selected.plugin.Manifest.Get("channels"), &channels)
		for _, channel := range channels {
			values, err := resolver.LoadServerUserConfig(ctx, selected.id, channel.String("server"))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			saved := protocol.NewJSONObject()
			if values != nil {
				saved = *values
			}
			title := channel.String("displayName")
			if value := channel.Get("displayName"); value == nil || string(value) == "null" {
				title = channel.String("server")
			}
			if err = add(channel.String("server"), title, mainObj(channel.Get("userConfig")), saved); err != nil {
				return protocol.JSONObject{}, err
			}
		}
	}
	return protocol.NewJSONObject(fieldMain("steps", steps)), nil
}

// Manage re-reads the real merged settings after the operation: an absent
// enabledPlugins entry passes the original !== false check, including after
// uninstall deleted that entry and the old saved options.
func (m *MainPrint) TerminalSelectedPluginPostConfiguration(ctx context.Context, selected *TerminalPluginSelection) (protocol.JSONObject, error) {
	if !m.validTerminalPluginSelection(selected) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return m.terminalPluginWorker(ctx, func(worker context.Context, _ *pluginCLIJobs) (protocol.JSONObject, error) {
		m.nativeSettings.mu.Lock()
		settings, err := m.nativeSettings.startup.GetInitialSettings()
		m.nativeSettings.mu.Unlock()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		enabled := true
		if settings != nil {
			enabled = string(mainObj(settings.Get("enabledPlugins")).Get(selected.id)) != "false"
		}
		result := protocol.NewJSONObject(fieldMain("enabled", enabled), fieldMain("steps", []protocol.JSONObject{}))
		if enabled {
			result, err = m.terminalSelectedPluginConfiguration(worker, selected, false)
			result.Set("enabled", []byte("true"))
		}
		return result, err
	})
}

func (m *MainPrint) SaveTerminalSelectedPluginConfiguration(ctx context.Context, selected *TerminalPluginSelection, server string, values protocol.JSONObject) error {
	if !m.validTerminalPluginSelection(selected) {
		return cooperative.ErrRestricted
	}
	_, err := m.terminalPluginOptionsCacheEdit(ctx, selected.id, server, func(worker context.Context, _ *pluginCLIJobs) (protocol.JSONObject, error) {
		schema := mainObj(selected.plugin.Manifest.Get("userConfig"))
		if server != "" {
			schema = protocol.NewJSONObject()
			var channels []protocol.JSONObject
			_ = json.Unmarshal(selected.plugin.Manifest.Get("channels"), &channels)
			for _, channel := range channels {
				if channel.String("server") == server {
					schema = mainObj(channel.Get("userConfig"))
					break
				}
			}
		}
		if len(schema.OwnProperties()) == 0 {
			return protocol.JSONObject{}, errors.New("Plugin configuration no longer available")
		}
		if server == "" {
			return protocol.NewJSONObject(), m.pluginOptions.Save(worker, selected.id, values, schema)
		}
		resolver, _, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		return protocol.NewJSONObject(), resolver.SaveServerUserConfig(worker, selected.id, server, values, schema)
	})
	return err
}
