package clibootstrap

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Manage derives one state per loaded plugin; ordinary CLI list intentionally
// retains every installation. Selection follows getPluginInstallationFromV2's
// exact original-cwd priority, including its first-entry fallback.
func terminalPluginManageRows(rows []protocol.JSONObject, loaded []pluginregistry.Loaded, installed, settings protocol.JSONObject, cwd string) []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, plugin := range loaded {
		marketplace := "local"
		if parts := strings.Split(plugin.Source, "@"); len(parts) > 1 && parts[1] != "" {
			marketplace = parts[1]
		}
		id := plugin.Name + "@" + marketplace
		var installations []protocol.JSONObject
		_ = json.Unmarshal(mainObj(installed.Get("plugins")).Get(id), &installations)
		scope := "user"
		var selected protocol.JSONObject
		for _, preferred := range []string{"local", "project", "user"} {
			for _, entry := range installations {
				if entry.String("scope") == preferred && (preferred == "user" || entry.String("projectPath") == cwd) {
					selected = entry
					break
				}
			}
			if selected.IsObject() {
				break
			}
		}
		if !selected.IsObject() && len(installations) > 0 {
			selected = installations[0]
		}
		if selected.IsObject() {
			scope = selected.String("scope")
		}
		if plugin.IsBuiltin {
			scope = "builtin"
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain(id)}, protocol.JSONField{Name: "scope", Value: quoteMain(scope)}, protocol.JSONField{Name: "installPath", Value: quoteMain(plugin.Path)})
		for _, candidate := range rows {
			if candidate.String("id") == id && candidate.String("scope") == scope && candidate.String("projectPath") == selected.String("projectPath") {
				row = candidate.Clone()
				break
			}
		}
		row.Set("name", quoteMain(plugin.Name))
		row.Set("manifest", rawMain(plugin.Manifest))
		row.Set("plugin", rawMain(plugin))
		row.Set("description", plugin.Manifest.Get("description"))
		row.Set("version", plugin.Manifest.Get("version"))
		row.Set("enabled", rawMain(string(mainObj(settings.Get("enabledPlugins")).Get(id)) != "false"))
		result = append(result, row)
	}
	return result
}
