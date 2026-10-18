package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Plugin dialogs share the session's settings, registry, options and credential
// producers. Network and physical file work yields this same Harness owner.
func (m *MainPrint) terminalPluginWorker(ctx context.Context, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.nativeSettings == nil || m.pluginOptions == nil {
			return cooperative.ErrClosed
		}
		pending, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
			jobs, err := newPluginCLIJobs(ctx, m.options.Native)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			defer jobs.Join()
			value, err := work(ctx, jobs)
			return value.Clone(), err
		})
		if err != nil {
			return err
		}
		result, err = pending.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
func (m *MainPrint) TerminalPluginSnapshot(ctx context.Context, available bool) (protocol.JSONObject, error) {
	return m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		p := m.nativeSettings
		snapshot, err := p.pluginCLIListSnapshot(worker)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		mcp, _, err := p.PluginMCP(m.pluginOptions, m.secure)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		rows, err := plugincli.InstalledJSON(worker, snapshot, plugincli.ListOptions{LoadMCP: func(ctx context.Context, plugin pluginregistry.Loaded) (*protocol.JSONObject, error) {
			return mcp.LoadPluginMCPServers(ctx, mainObj(rawMain(plugin)), nil)
		}})
		if err != nil {
			return protocol.JSONObject{}, err
		}
		// Failed or declared-but-not-materialized plugins remain visible in the
		// Errors tab, even when they have no installation row.
		policy, err := p.pluginCLIReadSettings(worker, "managed", "")
		if err != nil {
			return protocol.JSONObject{}, err
		}
		blocked := mainObj(policy.Get("enabledPlugins"))
		project, err := p.pluginCLIReadSettings(worker, "project", "")
		if err != nil {
			return protocol.JSONObject{}, err
		}
		projectEnabled := mainObj(project.Get("enabledPlugins"))
		loaded := append(append([]pluginregistry.Loaded{}, snapshot.Loaded.Enabled...), snapshot.Loaded.Disabled...)
		p.mu.Lock()
		initial, err := p.startup.GetInitialSettings()
		settings := protocol.NewJSONObject()
		if initial != nil {
			settings = initial.Clone()
		}
		p.mu.Unlock()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for i := range rows {
			for _, plugin := range loaded {
				if plugin.Source == rows[i].String("id") {
					rows[i].Set("manifest", rawMain(plugin.Manifest))
					rows[i].Set("mcpbSource", quoteMain(terminalPluginMCPBSource(plugin.Manifest)))
					rows[i].Set("hasMcpb", rawMain(terminalPluginHasMCPB(m.options.Native, plugin)))
					rows[i].Set("name", quoteMain(plugin.Name))
					rows[i].Set("description", plugin.Manifest.Get("description"))
					rows[i].Set("plugin", rawMain(plugin))
					break
				}
			}
			rows[i].Set("blocked", rawMain(string(blocked.Get(rows[i].String("id"))) == "false"))
			rows[i].Set("projectEnabled", rawMain(pluginregistry.IsEnabledSetting(projectEnabled.Get(rows[i].String("id")))))
		}
		manager, err := p.pluginCLIMarketplaces(jobs)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		known, err := manager.Known(worker)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		marketplaces := []protocol.JSONObject{}
		for _, property := range known.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			row := mainObj(property.Value)
			row.Set("name", quoteMain(name))
			marketplaces = append(marketplaces, row)
		}
		options := protocol.NewJSONObject(protocol.JSONField{Name: "installed", Value: rawMain(rows)}, protocol.JSONField{Name: "marketplaces", Value: rawMain(marketplaces)}, protocol.JSONField{Name: "errors", Value: rawMain(snapshot.Loaded.Errors)})
		manageRows := terminalPluginManageRows(rows, loaded, snapshot.Installed, settings, m.options.Native.CWD)
		for i := range manageRows {
			manageRows[i].Set("hasMcpb", rawMain(terminalPluginHasMCPB(m.options.Native, loaded[i])))
			manageRows[i].Set("blocked", rawMain(string(blocked.Get(manageRows[i].String("id"))) == "false"))
			manageRows[i].Set("projectEnabled", rawMain(pluginregistry.IsEnabledSetting(projectEnabled.Get(manageRows[i].String("id")))))
		}
		options.Set("manageInstalled", rawMain(manageRows))
		if err := p.populateTerminalPluginErrors(worker, known, &options); err != nil {
			return protocol.JSONObject{}, err
		}
		installation, err := m.NativePluginInstallationStatus(worker)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var initialErrors []protocol.JSONObject
		_ = json.Unmarshal(installation.Get("initialErrors"), &initialErrors)
		options.Set("errors", rawMain(terminalPluginMergedErrors(initialErrors, snapshot.Loaded.Errors)))
		options.Set("flaggedPlugins", rawMain(p.plugins.flagged.Snapshot()))
		failedMarketplaces := []protocol.JSONObject{}
		var marketplaceStates []protocol.JSONObject
		_ = json.Unmarshal(installation.Get("marketplaces"), &marketplaceStates)
		for _, state := range marketplaceStates {
			if state.String("status") == "failed" {
				failedMarketplaces = append(failedMarketplaces, state)
			}
		}
		options.Set("failedMarketplaces", rawMain(failedMarketplaces))
		global, err := m.Global.Get()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		env := m.options.Native.Environment
		disabled := env["NODE_ENV"] == "development" || nativeEnvTruthy(env["DISABLE_AUTOUPDATER"]) || env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" || global != nil && string(global.Get("autoUpdates")) == "false" && (global.String("installMethod") != "native" || string(global.Get("autoUpdatesProtectedForNative")) != "true")
		options.Set("autoUpdateAvailable", rawMain(!disabled || nativeEnvTruthy(env["FORCE_AUTOUPDATE_PLUGINS"])))
		if available {
			enabled := mainObj(settings.Get("enabledPlugins")).Clone()
			rows, err := manager.AvailableWithEntries(worker, snapshot.Installed, enabled)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			visible := []protocol.JSONObject{}
			for _, row := range rows {
				if string(blocked.Get(row.String("pluginId"))) != "false" {
					visible = append(visible, row)
				}
			}
			options.Set("available", rawMain(visible))
		}
		return options, nil
	})
}
func (m *MainPrint) TerminalPluginOperation(ctx context.Context, command plugincli.Command) (protocol.JSONObject, error) {
	worker := m.terminalPluginWorker
	if command.Action == "marketplace-remove" || command.Action == "install" || command.Action == "enable" || command.Action == "disable" || command.Action == "update" {
		worker = m.terminalPluginCacheEdit
	}
	if command.Action == "uninstall" {
		worker = func(ctx context.Context, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
			return m.terminalPluginUninstallCacheEdit(ctx, command.Argument, work)
		}
	}
	return worker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		var result plugincli.Result
		var err error
		var marketplaceName string
		switch {
		case command.Action == "validate":
			result, err = plugincli.RunValidate(worker, command.Argument, m.options.Native.CWD)
		case strings.HasPrefix(command.Action, "marketplace-"):
			manager, e := m.nativeSettings.pluginCLIMarketplaces(jobs)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if command.Action == "marketplace-add" {
				source, e := plugincli.ParseMarketplaceInput(command.Argument, m.options.Native.Home, m.options.Native.CWD, m.options.Platform)
				if e != nil {
					return protocol.JSONObject{}, e
				}
				if source == nil {
					return protocol.JSONObject{}, errors.New("Invalid marketplace source format. Try: owner/repo, https://..., or ./path")
				}
				var added pluginsapi.AddedMarketplace
				added, err = manager.Add(worker, *source, "user", nil)
				marketplaceName = added.Name
				result.Stdout = "Successfully added marketplace: " + added.Name
			} else if command.Action == "marketplace-auto-update" {
				err = manager.SetAutoUpdate(worker, command.Argument, command.JSON)
				result.Stdout = "Updated auto-update setting"
			} else {
				result, err = plugincli.RunMarketplace(worker, command, manager, m.options.Native.Home, m.options.Native.CWD, m.options.Platform)
			}
		default:
			service, e := m.nativeSettings.pluginCLIOperations(m.pluginOptions, jobs)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if command.Action == "install" {
				scope := command.Scope
				if scope == "" {
					scope = "user"
				}
				value, err := service.InstallInteractive(worker, command.Argument, scope)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain("✓ " + value.String("message"))}), nil
			}
			if command.Action == "enable" || command.Action == "disable" {
				value, err := service.SetEnabled(worker, command.Argument, command.Action == "enable", command.Scope, "")
				if err != nil {
					return protocol.JSONObject{}, err
				}
				return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain("✓ " + value.String("message"))}), nil
			}
			if command.Action == "update" {
				value, err := service.UpdateInteractive(worker, command.Argument, command.Scope, "")
				if err == nil && string(value.Get("alreadyUpToDate")) != "true" {
					err = m.nativeSettings.pluginCLIClear(worker)
				}
				return value, err
			}
			result, err = plugincli.RunOperation(worker, command, service)
		}
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if result.ExitCode != 0 {
			return protocol.JSONObject{}, errors.New(strings.TrimSpace(result.Stderr))
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain(strings.TrimSpace(result.Stdout))}, protocol.JSONField{Name: "marketplaceName", Value: quoteMain(marketplaceName)}), nil
	})
}

// Configuration values stay in their original top-level or per-server trust
// domain. Sensitive values are represented only as presence bits in a panel.
func (m *MainPrint) TerminalPluginConfiguration(ctx context.Context, id string, all bool) (protocol.JSONObject, error) {
	return m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		loaded, err := m.nativeSettings.LoadPlugins(worker, "", true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, plugin := range append(loaded.Enabled, loaded.Disabled...) {
			if plugin.Source == id || plugin.Repository == id {
				return m.terminalSelectedPluginConfiguration(worker, &TerminalPluginSelection{owner: m, id: id, plugin: plugin}, all)
			}
		}
		return protocol.JSONObject{}, fmt.Errorf("Plugin %q is no longer loaded", id)
	})
}
func (m *MainPrint) SaveTerminalPluginConfiguration(ctx context.Context, id, server string, values protocol.JSONObject) error {
	_, err := m.terminalPluginOptionsCacheEdit(ctx, id, server, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		loaded, err := m.nativeSettings.LoadPlugins(worker, "", true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, plugin := range append(loaded.Enabled, loaded.Disabled...) {
			if plugin.Source != id && plugin.Repository != id {
				continue
			}
			schema := mainObj(plugin.Manifest.Get("userConfig"))
			if server != "" {
				schema = protocol.NewJSONObject()
				var channels []protocol.JSONObject
				_ = json.Unmarshal(plugin.Manifest.Get("channels"), &channels)
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
				err = m.pluginOptions.Save(worker, id, values, schema)
			} else {
				mcp, _, e := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
				if e != nil {
					return protocol.JSONObject{}, e
				}
				err = mcp.SaveServerUserConfig(worker, id, server, values, schema)
			}
			return protocol.NewJSONObject(), err
		}
		return protocol.JSONObject{}, errors.New("Plugin is no longer loaded")
	})
	return err
}

func mainObj(value json.RawMessage) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(value)
	if !object.IsObject() {
		return protocol.NewJSONObject()
	}
	return object
}
func terminalPluginDataName(id string) string {
	var name strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			name.WriteRune(r)
		} else {
			name.WriteByte('-')
		}
	}
	return name.String()
}
