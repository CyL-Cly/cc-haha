package application

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalPluginErrorMessage(e protocol.JSONObject) string {
	v := e.String
	join := func(key string) string {
		var rows []json.RawMessage
		_ = json.Unmarshal(e.Get(key), &rows)
		values := []string{}
		for _, row := range rows {
			encoded, _ := protocol.JavaScriptStringJSON(row)
			var value string
			_ = json.Unmarshal(encoded, &value)
			values = append(values, value)
		}
		return strings.Join(values, ", ")
	}
	switch v("type") {
	case "path-not-found":
		return v("component") + " path not found: " + v("path")
	case "git-auth-failed":
		return "Git " + strings.ToUpper(v("authType")) + " authentication failed for " + v("gitUrl")
	case "git-timeout":
		return "Git " + v("operation") + " timed out for " + v("gitUrl")
	case "network-error":
		tail := ""
		if v("details") != "" {
			tail = ": " + v("details")
		}
		return "Network error accessing " + v("url") + tail
	case "manifest-parse-error":
		return "Failed to parse manifest at " + v("manifestPath") + ": " + v("parseError")
	case "manifest-validation-error":
		return "Invalid manifest at " + v("manifestPath") + ": " + join("validationErrors")
	case "plugin-not-found":
		return "Plugin \"" + v("pluginId") + "\" not found in marketplace \"" + v("marketplace") + "\""
	case "marketplace-not-found":
		return "Marketplace \"" + v("marketplace") + "\" not found"
	case "marketplace-load-failed":
		return "Failed to load marketplace \"" + v("marketplace") + "\": " + v("reason")
	case "mcp-config-invalid":
		return "Invalid MCP server config for \"" + v("serverName") + "\": " + v("validationError")
	case "hook-load-failed":
		return "Failed to load hooks from " + v("hookPath") + ": " + v("reason")
	case "component-load-failed":
		return "Failed to load " + v("component") + " from " + v("path") + ": " + v("reason")
	case "marketplace-blocked-by-policy":
		if string(e.Get("blockedByBlocklist")) == "true" {
			return "Marketplace \"" + v("marketplace") + "\" is blocked by enterprise policy"
		}
		return "Marketplace \"" + v("marketplace") + "\" is not in the allowed marketplace list"
	case "dependency-unsatisfied":
		state := "not installed"
		if v("reason") == "not-enabled" {
			state = "disabled"
		}
		return "Dependency \"" + v("dependency") + "\" is " + state
	case "lsp-config-invalid":
		return "Invalid LSP server config for \"" + v("serverName") + "\": " + v("validationError")
	case "lsp-server-start-failed":
		return "LSP server \"" + v("serverName") + "\" failed to start: " + v("reason")
	case "lsp-server-crashed":
		tail := "exit code unknown"
		if e.Get("exitCode") != nil && string(e.Get("exitCode")) != "null" {
			tail = "exit code " + string(e.Get("exitCode"))
		}
		if v("signal") != "" {
			tail = "signal " + v("signal")
		}
		return "LSP server \"" + v("serverName") + "\" crashed with " + tail
	case "lsp-request-timeout":
		return "LSP server \"" + v("serverName") + "\" timed out on " + v("method") + " after " + string(e.Get("timeoutMs")) + "ms"
	case "lsp-request-failed":
		return "LSP server \"" + v("serverName") + "\" " + v("method") + " failed: " + v("error")
	case "plugin-cache-miss":
		return "Plugin \"" + v("plugin") + "\" not cached at " + v("installPath")
	default:
		return pluginsapi.ErrorMessage(e)
	}
}
func terminalPluginErrorGuidance(e protocol.JSONObject) json.RawMessage {
	v := e.String
	text := ""
	join := func(key string) string {
		var values []string
		_ = json.Unmarshal(e.Get(key), &values)
		return strings.Join(values, ", ")
	}
	switch v("type") {
	case "path-not-found":
		text = "Check that the path in your manifest or marketplace config is correct"
	case "git-auth-failed":
		text = "Configure credentials or use SSH URL instead"
		if v("authType") == "ssh" {
			text = "Configure SSH keys or use HTTPS URL instead"
		}
	case "git-timeout", "network-error":
		text = "Check your internet connection and try again"
	case "manifest-parse-error":
		text = "Check manifest file syntax in the plugin directory"
	case "manifest-validation-error":
		text = "Check manifest file follows the required schema"
	case "plugin-not-found":
		text = "Plugin may not exist in marketplace \"" + v("marketplace") + "\""
	case "marketplace-not-found":
		text = "Add the marketplace first using /plugin marketplace add"
		if value := join("availableMarketplaces"); value != "" {
			text = "Available marketplaces: " + value
		}
	case "mcp-config-invalid":
		text = "Check MCP server configuration in .mcp.json or manifest"
	case "mcp-server-suppressed-duplicate":
		text = "Remove \"" + v("duplicateOf") + "\" from your MCP config if you want the plugin's version instead"
		if strings.HasPrefix(v("duplicateOf"), "plugin:") {
			name := strings.Split(v("duplicateOf"), ":")[1]
			text = "Disable plugin \"" + name + "\" if you want this plugin's version instead"
		}
	case "hook-load-failed":
		text = "Check hooks.json file syntax and structure"
	case "component-load-failed":
		text = "Check " + v("component") + " directory structure and file permissions"
	case "mcpb-download-failed":
		text = "Check your internet connection and URL accessibility"
	case "mcpb-extract-failed":
		text = "Verify the MCPB file is valid and not corrupted"
	case "mcpb-invalid-manifest":
		text = "Contact the plugin author about the invalid manifest"
	case "marketplace-blocked-by-policy":
		text = "Contact your administrator to configure allowed marketplace sources"
		if string(e.Get("blockedByBlocklist")) == "true" {
			text = "This marketplace source is explicitly blocked by your administrator"
		} else if value := join("allowedSources"); value != "" {
			text = "Allowed sources: " + value
		}
	case "dependency-unsatisfied":
		text = "Install \"" + v("dependency") + "\" or uninstall \"" + v("plugin") + "\""
		if v("reason") == "not-enabled" {
			text = "Enable \"" + v("dependency") + "\" or uninstall \"" + v("plugin") + "\""
		}
	case "lsp-config-invalid":
		text = "Check LSP server configuration in the plugin manifest"
	case "lsp-server-start-failed", "lsp-server-crashed", "lsp-request-timeout", "lsp-request-failed":
		text = "Check LSP server logs with --debug for details"
	case "plugin-cache-miss":
		text = "Run /plugins to refresh the plugin cache"
	default:
		return raw(nil)
	}
	return raw(text)
}
func terminalPluginErrorName(e protocol.JSONObject) string {
	if e.String("pluginId") != "" {
		return e.String("pluginId")
	}
	if e.String("plugin") != "" {
		return e.String("plugin")
	}
	if strings.Contains(e.String("source"), "@") {
		return strings.Split(e.String("source"), "@")[0]
	}
	return ""
}
func terminalPluginErrorRows(snapshot protocol.JSONObject) []protocol.JSONObject {
	settings := obj(snapshot.Get("errorSettings"))
	scopes := obj(snapshot.Get("pluginScopes"))
	errors := array(snapshot.Get("errors"))
	rows := []protocol.JSONObject{}
	record := func(label, message string, guidance json.RawMessage, action protocol.JSONObject, scope string) {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "label", Value: raw(label)}, protocol.JSONField{Name: "message", Value: raw(message)}, protocol.JSONField{Name: "action", Value: raw(action)})
		if guidance != nil {
			row.Set("guidance", guidance)
		}
		if scope != "" {
			row.Set("scope", raw(scope))
		}
		rows = append(rows, row)
	}
	none := obj([]byte(`{"kind":"none"}`))
	marketAction := func(name string) (protocol.JSONObject, string) {
		sources := []protocol.JSONObject{}
		for _, scope := range []string{"user", "project", "local"} {
			if value := obj(obj(settings.Get(scope)).Get("extraKnownMarketplaces")).Get(name); value != nil && sourceSDKTruthy(value) {
				sources = append(sources, protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: raw(map[string]string{"user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope])}, protocol.JSONField{Name: "scope", Value: raw(scope)}))
			}
		}
		policy := sourceSDKTruthy(obj(obj(settings.Get("managed")).Get("extraKnownMarketplaces")).Get(name))
		scope := ""
		if policy {
			scope = "managed"
		} else if len(sources) > 0 {
			scope = sources[0].String("scope")
		}
		action := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("navigate")}, protocol.JSONField{Name: "tab", Value: raw("marketplaces")}, protocol.JSONField{Name: "viewState", Value: raw(map[string]any{"type": "manage-marketplaces", "targetMarketplace": name, "action": "remove"})})
		if len(sources) > 0 {
			action = protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("remove-extra-marketplace")}, protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "sources", Value: raw(sources)})
		} else if policy {
			action = protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("managed-only")}, protocol.JSONField{Name: "name", Value: raw(name)})
		}
		return action, scope
	}
	transient := func(e protocol.JSONObject) bool {
		return e.String("type") == "git-auth-failed" || e.String("type") == "git-timeout" || e.String("type") == "network-error"
	}
	isMarket := func(e protocol.JSONObject) bool {
		return e.String("type") == "marketplace-not-found" || e.String("type") == "marketplace-load-failed" || e.String("type") == "marketplace-blocked-by-policy"
	}
	for _, e := range errors {
		if transient(e) {
			label := e.String("source")
			if e.Get("pluginId") != nil {
				label = e.String("pluginId")
			} else if e.Get("plugin") != nil {
				label = e.String("plugin")
			}
			record(label, terminalPluginErrorMessage(e), raw("Restart to retry loading plugins"), none, "")
		}
	}
	shown := map[string]bool{}
	for _, e := range array(snapshot.Get("failedMarketplaces")) {
		name := e.String("name")
		shown[name] = true
		action, scope := marketAction(name)
		guidance := json.RawMessage(nil)
		if action.String("kind") == "managed-only" {
			guidance = raw("Managed by your organization — contact your admin")
		}
		message := e.String("error")
		if e.Get("error") == nil {
			message = "Installation failed"
		}
		record(name, message, guidance, action, scope)
	}
	for _, e := range errors {
		if !isMarket(e) {
			continue
		}
		name := e.String("source")
		if e.Get("marketplace") != nil {
			name = e.String("marketplace")
		}
		if shown[name] {
			continue
		}
		shown[name] = true
		action, scope := marketAction(name)
		guidance := terminalPluginErrorGuidance(e)
		if action.String("kind") == "managed-only" {
			guidance = raw("Managed by your organization — contact your admin")
		}
		record(name, terminalPluginErrorMessage(e), guidance, action, scope)
	}
	for _, e := range array(snapshot.Get("brokenMarketplaces")) {
		name := e.String("name")
		if shown[name] {
			continue
		}
		shown[name] = true
		record(name, e.String("error"), nil, protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("remove-installed-marketplace")}, protocol.JSONField{Name: "name", Value: raw(name)}), "")
	}
	shown = map[string]bool{}
	for _, e := range errors {
		if transient(e) || isMarket(e) {
			continue
		}
		name := terminalPluginErrorName(e)
		if name == "" {
			continue
		}
		if shown[name] {
			continue
		}
		shown[name] = true
		label := name
		if e.Get("marketplace") != nil && e.String("marketplace") != "" {
			label += " @ " + e.String("marketplace")
		}
		scope := scopes.String(e.String("source"))
		if scope == "" {
			scope = scopes.String(name)
		}
		record(label, terminalPluginErrorMessage(e), terminalPluginErrorGuidance(e), protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("navigate")}, protocol.JSONField{Name: "tab", Value: raw("installed")}, protocol.JSONField{Name: "viewState", Value: raw(map[string]any{"type": "manage-plugins", "targetPlugin": name, "action": "uninstall"})}), scope)
	}
	for _, e := range errors {
		if !transient(e) && !isMarket(e) && terminalPluginErrorName(e) == "" {
			record(e.String("source"), terminalPluginErrorMessage(e), terminalPluginErrorGuidance(e), none, "")
		}
	}
	return rows
}

func terminalFailedPluginRows(snapshot protocol.JSONObject) []protocol.JSONObject {
	matched := map[string]bool{}
	for _, row := range array(snapshot.Get("installed")) {
		if row.Get("manifest") != nil {
			matched[row.String("id")] = true
		}
	}
	grouped := map[string][]protocol.JSONObject{}
	order := []string{}
	for _, e := range array(snapshot.Get("errors")) {
		id := e.String("source")
		if id == "" || matched[id] {
			continue
		}
		if e.String("plugin") != "" {
			found := false
			for key := range matched {
				found = found || strings.Split(key, "@")[0] == e.String("plugin")
			}
			if found {
				continue
			}
		}
		if _, exists := grouped[id]; !exists {
			order = append(order, id)
		}
		grouped[id] = append(grouped[id], e)
	}
	scopes := obj(snapshot.Get("pluginScopes"))
	rows := []protocol.JSONObject{}
	for _, id := range order {
		name, market, qualified := strings.Cut(id, "@")
		if !qualified {
			market = "unknown"
		}
		scope := scopes.String(id)
		if scope == "" || scope == "flag" {
			scope = "user"
		}
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: raw(id)}, protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "marketplace", Value: raw(market)}, protocol.JSONField{Name: "scope", Value: raw(scope)}, protocol.JSONField{Name: "failed", Value: raw(true)}, protocol.JSONField{Name: "errors", Value: raw(grouped[id])}))
	}
	return rows
}
