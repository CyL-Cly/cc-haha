package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpb"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalPluginMCPBSource(manifest protocol.JSONObject) string {
	value := manifest.Get("mcpServers")
	var source string
	if json.Unmarshal(value, &source) == nil && mcpb.IsSource(source) {
		return source
	}
	var sources []json.RawMessage
	_ = json.Unmarshal(value, &sources)
	for _, item := range sources {
		if json.Unmarshal(item, &source) == nil && mcpb.IsSource(source) {
			return source
		}
	}
	return ""
}

func terminalPluginHasMCPB(n *NativeBoundary, plugin pluginregistry.Loaded) bool {
	if terminalPluginMCPBSource(plugin.Manifest) != "" {
		return true
	}
	// The original legacy-cache detector reads this exact raw parent manifest,
	// bypassing catalog schema validation. It only reveals the Configure row;
	// Configure itself still requires a source in the actual plugin manifest.
	data, err := n.readFile(filepath.Join(plugin.Path, "..", ".claude-plugin", "marketplace.json"))
	if err != nil {
		return false
	}
	var rows []protocol.JSONObject
	if json.Unmarshal(mainObj(protocol.DecodeJavaScriptUTF8(data)).Get("plugins"), &rows) != nil {
		return false
	}
	for _, row := range rows {
		if row.String("name") == plugin.Name {
			return terminalPluginMCPBSource(row) != ""
		}
	}
	return false
}

// Manage Configure uses the same MCPB loader, option domains and extraction
// cache as actual plugin MCP discovery. Opening forces a dialog even when the
// current configuration already validates; saving follows the original reload.
func (m *MainPrint) TerminalPluginMCPBConfiguration(ctx context.Context, id string, provided *protocol.JSONObject) (protocol.JSONObject, error) {
	return m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		loaded, err := m.nativeSettings.LoadPlugins(worker, "", true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		_, loader, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, plugin := range append(append([]pluginregistry.Loaded{}, loaded.Enabled...), loaded.Disabled...) {
			if plugin.Source != id && plugin.Repository != id {
				continue
			}
			source := terminalPluginMCPBSource(plugin.Manifest)
			if source == "" {
				return protocol.JSONObject{}, errors.New("No MCPB file found in plugin")
			}
			result, err := loader.Load(worker, source, plugin.Path, id, nil, provided, provided == nil)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			if provided != nil {
				return protocol.NewJSONObject(), nil
			}
			if !result.NeedsConfig {
				return protocol.JSONObject{}, errors.New("Failed to load MCPB for configuration")
			}
			visible, present := result.ExistingConfig.Clone(), protocol.NewJSONObject()
			for _, property := range result.ConfigSchema.OwnProperties() {
				var key string
				_ = json.Unmarshal(property.NameJSON, &key)
				if string(mainObj(property.Value).Get("sensitive")) == "true" {
					if visible.Get(key) != nil {
						present.Set(key, rawMain(true))
					}
					visible.Delete(key)
				}
			}
			step := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quoteMain("mcpb")}, protocol.JSONField{Name: "title", Value: quoteMain(result.Manifest.String("name"))}, protocol.JSONField{Name: "server", Value: quoteMain(result.Manifest.String("name"))}, protocol.JSONField{Name: "schema", Value: rawMain(result.ConfigSchema)}, protocol.JSONField{Name: "values", Value: rawMain(visible)}, protocol.JSONField{Name: "savedSensitive", Value: rawMain(present)})
			return protocol.NewJSONObject(protocol.JSONField{Name: "steps", Value: rawMain([]protocol.JSONObject{step})}), nil
		}
		return protocol.JSONObject{}, errors.New("Plugin is no longer loaded")
	})
}
