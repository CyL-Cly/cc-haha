package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Config activation consumes the cached plugin snapshot and the same secure /
// settings option merger as MCP and Agent. A declaration alone never spawns a
// process, and one plugin's failed interpolation does not hide the others.
func (m *MainPrint) nativeLSPConfigurations(ctx context.Context) ([]lspruntime.ServerConfig, error) {
	loaded, err := m.nativeSettings.LoadPlugins(ctx, "", false)
	if err != nil {
		return nil, err
	}
	resolver, _, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
	if err != nil {
		return nil, err
	}
	rows := make([][]lspruntime.ServerConfig, len(loaded.Enabled))
	failures := make([]error, len(rows))
	var group sync.WaitGroup
	for i, plugin := range loaded.Enabled {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			servers := plugin.LSPServers
			if servers == nil {
				errors := []protocol.JSONObject{}
				var err error
				servers, err = pluginsapi.LoadLSP(ctx, plugin, &errors)
				if err != nil {
					failures[i] = err
					return
				}
			}
			if servers == nil {
				return
			}
			var options *protocol.JSONObject
			if planRawTruthy(plugin.Manifest.Get("userConfig")) {
				values, err := m.pluginOptions.Load(ctx, plugin.Source)
				if err != nil {
					failures[i] = err
					return
				}
				options = &values
			}
			definition := protocol.NewJSONObject(fieldMain("path", plugin.Path), fieldMain("source", plugin.Source))
			for _, entry := range servers.OwnProperties() {
				var name string
				_ = json.Unmarshal(entry.NameJSON, &name)
				config := mainObj(entry.Value)
				// LSP shares the original scalar substitutions with MCP's stdio
				// branch; only workspaceFolder is an additional LSP scalar field.
				missing := []string{}
				resolved, err := resolver.Resolve(ctx, config, definition, options, nil, "", "", &missing)
				if err != nil {
					failures[i] = err
					return
				}
				if workspace := config.Get("workspaceFolder"); planRawTruthy(workspace) {
					workspace, err = resolver.ResolveValue(ctx, workspace, definition, options, &missing)
					if err != nil {
						failures[i] = err
						return
					}
					resolved.Set("workspaceFolder", workspace)
				}
				if len(missing) > 0 {
					seen := map[string]bool{}
					unique := []string{}
					for _, name := range missing {
						if !seen[name] {
							seen[name] = true
							unique = append(unique, name)
						}
					}
					message := "Missing environment variables in plugin LSP config: " + strings.Join(unique, ", ")
					m.reportNativeLSPError(errors.New(message))
					m.reportNativeLSPDebugLevel(message, "WARN")
				}
				resolved.Set("scope", quoteMain("dynamic"))
				resolved.Set("source", quoteMain(plugin.Name))
				rows[i] = append(rows[i], lspruntime.ServerConfig{Name: "plugin:" + plugin.Name + ":" + name, Config: resolved})
			}
		}(i)
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	all := []lspruntime.ServerConfig{}
	positions := map[string]int{}
	for i, row := range rows {
		if failures[i] != nil {
			name := errorlog.Details(failures[i]).Name
			if name == "" {
				name = "Error"
			}
			m.reportNativeLSPDebugLevel("Failed to load LSP servers for plugin "+loaded.Enabled[i].Name+": "+name+": "+errorlog.Text(failures[i]), "ERROR")
			continue
		}
		for _, entry := range row {
			if at, exists := positions[entry.Name]; exists {
				all[at] = entry
			} else {
				positions[entry.Name] = len(all)
				all = append(all, entry)
			}
		}
	}
	return all, nil
}
