package pluginsapi

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func ErrorMessage(e protocol.JSONObject) string {
	v := func(key string) string { return e.String(key) }
	switch v("type") {
	case "generic-error":
		return v("error")
	case "path-not-found":
		return "Path not found: " + v("path") + " (" + v("component") + ")"
	case "git-auth-failed":
		return "Git authentication failed (" + v("authType") + "): " + v("gitUrl")
	case "git-timeout":
		return "Git " + v("operation") + " timeout: " + v("gitUrl")
	case "network-error":
		suffix := ""
		if v("details") != "" {
			suffix = " - " + v("details")
		}
		return "Network error: " + v("url") + suffix
	case "manifest-parse-error":
		return "Manifest parse error: " + v("parseError")
	case "manifest-validation-error":
		values := []string{}
		for _, value := range arr(e.Get("validationErrors")) {
			values = append(values, stringValue(value))
		}
		return "Manifest validation failed: " + strings.Join(values, ", ")
	case "plugin-not-found":
		return "Plugin " + v("pluginId") + " not found in marketplace " + v("marketplace")
	case "marketplace-not-found":
		return "Marketplace " + v("marketplace") + " not found"
	case "marketplace-load-failed":
		return "Marketplace " + v("marketplace") + " failed to load: " + v("reason")
	case "mcp-config-invalid":
		return "MCP server " + v("serverName") + " invalid: " + v("validationError")
	case "mcp-server-suppressed-duplicate":
		dup := "already-configured \"" + v("duplicateOf") + "\""
		if strings.HasPrefix(v("duplicateOf"), "plugin:") {
			parts := strings.Split(v("duplicateOf"), ":")
			name := "?"
			if len(parts) > 1 {
				name = parts[1]
			}
			dup = "server provided by plugin \"" + name + "\""
		}
		return "MCP server \"" + v("serverName") + "\" skipped — same command/URL as " + dup
	case "hook-load-failed":
		return "Hook load failed: " + v("reason")
	case "component-load-failed":
		return v("component") + " load failed from " + v("path") + ": " + v("reason")
	case "mcpb-download-failed":
		return "Failed to download MCPB from " + v("url") + ": " + v("reason")
	case "mcpb-extract-failed":
		return "Failed to extract MCPB " + v("mcpbPath") + ": " + v("reason")
	case "mcpb-invalid-manifest":
		return "MCPB manifest invalid at " + v("mcpbPath") + ": " + v("validationError")
	case "lsp-config-invalid":
		return "Plugin \"" + v("plugin") + "\" has invalid LSP server config for \"" + v("serverName") + "\": " + v("validationError")
	case "lsp-server-start-failed":
		return "Plugin \"" + v("plugin") + "\" failed to start LSP server \"" + v("serverName") + "\": " + v("reason")
	case "lsp-server-crashed":
		tail := "exit code " + string(e.Get("exitCode"))
		if e.Get("exitCode") == nil || string(e.Get("exitCode")) == "null" {
			tail = "exit code unknown"
		}
		if v("signal") != "" {
			tail = "signal " + v("signal")
		}
		return "Plugin \"" + v("plugin") + "\" LSP server \"" + v("serverName") + "\" crashed with " + tail
	case "lsp-request-timeout":
		return "Plugin \"" + v("plugin") + "\" LSP server \"" + v("serverName") + "\" timed out on " + v("method") + " request after " + string(e.Get("timeoutMs")) + "ms"
	case "lsp-request-failed":
		return "Plugin \"" + v("plugin") + "\" LSP server \"" + v("serverName") + "\" " + v("method") + " request failed: " + v("error")
	case "marketplace-blocked-by-policy":
		if string(e.Get("blockedByBlocklist")) == "true" {
			return "Marketplace '" + v("marketplace") + "' is blocked by enterprise policy"
		}
		return "Marketplace '" + v("marketplace") + "' is not in the allowed marketplace list"
	case "dependency-unsatisfied":
		hint := "not found in any configured marketplace"
		if v("reason") == "not-enabled" {
			hint = "disabled — enable it or remove the dependency"
		}
		return "Dependency \"" + v("dependency") + "\" is " + hint
	case "plugin-cache-miss":
		return "Plugin \"" + v("plugin") + "\" not cached at " + v("installPath") + " — run /plugins to refresh"
	}
	return ""
}
