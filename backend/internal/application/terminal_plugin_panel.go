package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalPluginPanelState struct {
	tab, level, target, action, scope, marketplace, search string
	selected                                               protocol.JSONObject
	selection                                              *clibootstrap.TerminalPluginSelection
	steps                                                  []protocol.JSONObject
	step                                                   int
	configurationReturnsToDetail                           bool
	postEnableConfiguration                                bool
	selectedForInstall                                     map[string]protocol.JSONObject
	available                                              []protocol.JSONObject
	pendingToggles                                         map[string]bool
	pendingUpdates                                         map[string]bool
	lastSelected                                           string
}

func parseTerminalPluginArgs(args string) *terminalPluginPanelState {
	p := &terminalPluginPanelState{tab: "discover", scope: "user"}
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return p
	}
	command := strings.ToLower(parts[0])
	target := ""
	if len(parts) > 1 {
		target = parts[1]
	}
	switch command {
	case "help", "--help", "-h":
		p.level = "help"
	case "install", "i":
		if strings.Contains(target, "@") {
			segments := strings.Split(target, "@")
			p.target = segments[0] + "@" + segments[1]
		} else if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "file://") || strings.ContainsAny(target, "/\\") {
			p.marketplace = target
		} else {
			p.target = target
		}
	case "manage":
		p.tab = "installed"
	case "uninstall", "enable", "disable":
		p.tab, p.target, p.action = "installed", target, command
	case "validate":
		p.level = "validate"
		p.target = strings.TrimSpace(strings.Join(parts[1:], " "))
	case "marketplace", "market":
		p.tab = "marketplaces"
		if len(parts) > 1 {
			p.action = strings.ToLower(parts[1])
		}
		if len(parts) > 2 {
			p.target = strings.Join(parts[2:], " ")
		}
		if p.action == "rm" {
			p.action = "remove"
		}
		switch p.action {
		case "add":
			p.level = "add-marketplace"
		case "remove", "update", "list":
		default:
			p.action = ""
		}
	}
	return p
}
func (s *Session) initializeTerminalPluginPanel(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit) (bool, error) {
	p := state.pluginPanel
	report := func(message string) (bool, error) {
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.Notice, Text: message})
		}
		return true, nil
	}
	if p.level == "validate" {
		result, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "validate", Argument: p.target})
		if err != nil {
			return report(err.Error())
		}
		return report(result.String("message"))
	}
	if p.tab == "marketplaces" && p.action == "list" {
		snapshot, err := s.process.TerminalPluginSnapshot(ctx, false)
		if err != nil {
			return true, err
		}
		rows := array(snapshot.Get("marketplaces"))
		if len(rows) == 0 {
			return report("No marketplaces configured")
		}
		names := []string{}
		for _, row := range rows {
			names = append(names, "  • "+row.String("name"))
		}
		return report("Configured marketplaces:\n" + strings.Join(names, "\n"))
	}
	input := p.target
	if p.level != "add-marketplace" {
		input = p.marketplace
	}
	if input != "" && (p.level == "add-marketplace" || strings.ContainsAny(input, "/\\")) {
		result, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "marketplace-add", Argument: input, Scope: "user"})
		if err != nil {
			state.props.Notice = err.Error()
			return false, nil
		}
		state.props.Notice = result.String("message")
		if p.level == "add-marketplace" {
			p.level, p.tab, p.target = "", "marketplaces", result.String("marketplaceName")
		} else {
			p.marketplace = result.String("marketplaceName")
		}
	}
	return false, nil
}
func (s *Session) refreshTerminalPluginPanel(ctx context.Context, state *terminalLocalPanelState) error {
	p := &state.props
	p.Rows, p.Fields, p.Actions = nil, nil, nil
	p.DismissAction = ""
	p.RawInputKeys = nil
	p.PluginOptions = false
	p.ConfirmationOnly = false
	p.MCPCatalog = false
	p.MCPAuthentication = false
	p.MCPExitScope = ""
	p.SubmitAction = ""
	p.PreserveFields = false
	p.SelectedRow = ""
	state.rows = map[string]protocol.JSONObject{}
	state.form = ""
	panel := state.pluginPanel
	if panel == nil {
		panel = &terminalPluginPanelState{tab: "discover", scope: "user"}
		state.pluginPanel = panel
	}
	if panel.level == "mcp-detail" {
		return s.refreshTerminalMCPPanel(ctx, state)
	}
	p.HideDetails = true
	p.Title = "Plugins · " + panel.tab
	p.Description = "Make sure you trust a plugin before installing, updating, or using it. Anthropic cannot verify the software included in plugins."
	add := func(id, label, detail string, row protocol.JSONObject, action string) {
		state.rows[id] = row.Clone()
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: id, Label: label, Detail: detail, Actions: []tui.LocalPanelAction{{Key: "enter", Name: action, Label: "select"}}})
	}
	p.Actions = []tui.LocalPanelAction{{Key: "tab", Name: "plugin-tab", Label: "switch tab"}, {Key: "/", Name: "plugin-search", Label: "search"}, {Key: "r", Name: "refresh", Label: "refresh"}}
	switch panel.level {
	case "help":
		p.Description = "/plugin install [plugin@marketplace] · /plugin manage\n/plugin enable|disable|uninstall [plugin]\n/plugin marketplace add|remove|update|list [target]\n/plugin validate [path]\n/reload-plugins activates pending changes"
		return nil
	case "add-marketplace", "validate", "search":
		p.DismissAction = "plugin-back"
		state.form = "plugin-input"
		p.Actions = nil
		value := panel.target
		label := "Manifest path"
		if panel.level == "add-marketplace" {
			label = "Marketplace source (owner/repo, URL or path)"
		}
		if panel.level == "search" {
			label = "Search"
			value = panel.search
		}
		p.Fields = []tui.LocalPanelField{{Name: "value", Label: label, Value: value}}
		return nil
	case "configuration":
		if panel.step >= len(panel.steps) {
			panel.level = ""
			if panel.configurationReturnsToDetail {
				panel.level = "detail"
				panel.configurationReturnsToDetail = false
			}
			panel.steps = nil
			return s.refreshTerminalPluginPanel(ctx, state)
		}
		p.PluginOptions = true
		step := panel.steps[panel.step]
		schema, values := obj(step.Get("schema")), obj(step.Get("values"))
		p.Title = "Configure " + step.String("title")
		p.Description = "Plugin options"
		if step.String("kind") == "mcpb" {
			p.Description = "Plugin: " + panel.selected.String("name")
		}
		p.DismissAction = "plugin-back"
		state.form = "plugin-options"
		p.Actions = nil
		for _, property := range schema.OwnProperties() {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			field := obj(property.Value)
			title := field.String("title")
			if title == "" {
				title = key
			}
			if string(field.Get("required")) == "true" {
				title += " *"
			}
			value := ""
			var valueJSON json.RawMessage
			saved, err := values.GetPropertyJSON(property.NameJSON)
			if err != nil {
				return err
			}
			if string(field.Get("sensitive")) != "true" && saved != nil {
				encoded, err := protocol.JavaScriptStringJSON(saved)
				if err != nil {
					return err
				}
				valueJSON = encoded
				_ = json.Unmarshal(encoded, &value)
			}
			p.Fields = append(p.Fields, tui.LocalPanelField{Name: terminalPluginFieldBinding(schema, property.NameJSON), Label: title, Value: value, ValueJSON: valueJSON, Description: field.String("description"), Sensitive: string(field.Get("sensitive")) == "true"})
		}
		return nil
	case "confirm-project-uninstall":
		p.Title = panel.selected.String("name") + " is enabled in .claude/settings.json (shared with your team)"
		p.Description = "Disable it just for you in .claude/settings.local.json?\nThis has the same effect as uninstalling, without affecting other contributors."
		p.DismissAction = "plugin-back"
		p.ConfirmationOnly = true
		p.Actions = []tui.LocalPanelAction{{Key: "confirm:yes", Name: "plugin-disable-local", Label: "disable"}, {Key: "confirm:no", Name: "plugin-back", Label: "cancel"}}
		return nil
	case "confirm-data-cleanup":
		p.Title = panel.selected.String("name") + " has " + obj(panel.selected.Get("dataSize")).String("human") + " of persistent data"
		p.Description = "Delete it along with the plugin?\n" + obj(panel.selected.Get("dataSize")).String("path")
		p.DismissAction = "plugin-back"
		p.Actions = nil
		p.RawInputKeys = []string{"y", "n", "Y", "N", "enter"}
		p.Actions = []tui.LocalPanelAction{{Key: "y", Name: "plugin-remove-data", Label: "delete data"}, {Key: "n", Name: "plugin-keep-data", Label: "keep data"}, {Key: "Y", Name: "plugin-remove-data"}, {Key: "N", Name: "plugin-keep-data"}}
		p.Description += "\ny to delete · n to keep · Esc cancels"
		return nil
	case "failed-detail":
		row := panel.selected
		p.Title = row.String("name") + " @ " + row.String("marketplace") + " (" + row.String("scope") + ")"
		p.DismissAction = "plugin-back"
		p.Actions = nil
		p.Description = "Failed to load"
		if failures := array(row.Get("errors")); len(failures) > 0 {
			p.Description = terminalPluginErrorMessage(failures[0])
		}
		if row.String("scope") == "managed" {
			p.Description += "\nManaged by your organization — contact your admin"
		} else {
			add("remove", "Remove", "", row, "plugin-remove-failed")
		}
		return nil
	case "flagged-detail":
		row := panel.selected
		p.Title = row.String("name") + " @ " + row.String("marketplace")
		p.Description = "Status: Removed\nRemoved from marketplace · reason: delisted\nRemoved from marketplace\nFlagged on " + terminalPluginFlagDate(row.Get("flaggedAt"))
		p.DismissAction = "plugin-back"
		p.Actions = []tui.LocalPanelAction{{Key: "enter", Name: "plugin-dismiss-flagged", Label: "dismiss"}}
		return nil
	case "detail":
		row := panel.selected
		p.DismissAction = "plugin-back"
		p.Title = row.String("id")
		p.Description += "\n" + row.String("description") + "\nVersion: " + row.String("version") + " · Scope: " + row.String("scope") + "\n" + row.String("installPath")
		p.Actions = nil
		operation := "install"
		if row.Get("enabled") != nil {
			operation = "enable"
			if string(row.Get("enabled")) == "true" {
				operation = "disable"
			}
		}
		if row.Get("enabled") == nil {
			add("install", "Install for you (user scope)", "", row, "plugin-install-user")
			add("install-project", "Install for all collaborators on this repository (project scope)", "", row, "plugin-install-project")
			add("install-local", "Install for you, in this repo only (local scope)", "", row, "plugin-install-local")
		} else {
			add(operation, strings.Title(operation)+" plugin", "", row, "plugin-"+operation)
		}
		if row.Get("enabled") != nil && row.String("scope") != "builtin" {
			label := "Mark for update"
			if panel.pendingUpdates[row.String("id")] {
				label = "Unmark for update"
			}
			add("update", label, "", row, "plugin-mark-update")
		}
		if row.Get("enabled") != nil && row.String("scope") != "builtin" && string(row.Get("hasMcpb")) == "true" {
			add("mcpb", "Configure", "", row, "plugin-configure-mcpb")
		}
		if row.Get("enabled") != nil && len(obj(obj(row.Get("manifest")).Get("userConfig")).OwnProperties()) > 0 && row.String("scope") != "builtin" {
			add("options", "Configure options", "", row, "plugin-configure")
		}
		if row.Get("enabled") != nil && row.String("scope") != "builtin" {
			add("update-now", "Update now", "", row, "plugin-update")
			add("uninstall", "Uninstall", "", row, "plugin-uninstall")
		}
		entry := obj(row.Get("entry"))
		if row.Get("enabled") != nil {
			entry = obj(row.Get("manifest"))
		}
		if entry.String("homepage") != "" {
			add("homepage", "Open homepage", "", row, "plugin-homepage")
		}
		if row.Get("enabled") != nil && entry.String("repository") != "" {
			add("repository", "View repository", "", row, "plugin-repository")
		} else if source := obj(entry.Get("source")); source.String("source") == "github" && source.String("repo") != "" {
			add("github", "View on GitHub", "", row, "plugin-github")
		}
		add("back", "Back to plugin list", "", row, "plugin-back")
		return nil
	case "market-detail":
		row := panel.selected
		p.DismissAction = "plugin-back"
		p.Title = "Marketplace · " + row.String("name")
		p.Description += "\n" + row.String("installLocation") + "\nLast updated: " + row.String("lastUpdated")
		p.Actions = nil
		add("browse", "Browse plugins", "", row, "plugin-browse")
		add("update", "Update marketplace", "", row, "plugin-market-update")
		if string(row.Get("autoUpdateAvailable")) == "true" {
			label := "Enable auto-update"
			if string(row.Get("autoUpdate")) == "true" {
				label = "Disable auto-update"
			}
			add("auto-update", label, "", row, "plugin-market-auto-update")
		}
		add("remove", "Remove marketplace", "", row, "plugin-market-remove")
		return nil
	case "confirm-market-remove":
		p.Title = "Remove marketplace?"
		p.Description = panel.selected.String("name") + " and its enabled plugin declarations will be removed."
		p.DismissAction = "plugin-back"
		p.Actions = nil
		add("remove", "Remove marketplace", "", panel.selected, "plugin-market-remove-confirmed")
		add("cancel", "Cancel", "", panel.selected, "plugin-back")
		return nil
	}
	snapshot, err := s.process.TerminalPluginSnapshot(ctx, panel.tab == "discover")
	if err != nil {
		return err
	}
	matches := func(value string) bool {
		return panel.search == "" || strings.Contains(strings.ToLower(value), strings.ToLower(panel.search))
	}
	switch panel.tab {
	case "installed":
		p.MCPCatalog = true
		p.SelectedRow = panel.lastSelected
		mcp, err := s.process.TerminalMCPMenuSnapshot(ctx)
		if err != nil {
			return err
		}
		rows, err := terminalPluginUnifiedInstalled(snapshot, array(mcp.Get("servers")))
		if err != nil {
			return err
		}
		flaggedIDs := []json.RawMessage{}
		for _, row := range rows {
			if row.String("itemType") == "flagged-plugin" {
				flaggedIDs = append(flaggedIDs, row.Get("id"))
			}
		}
		if len(flaggedIDs) > 0 {
			if err := s.process.MarkTerminalFlaggedPluginsSeenJSON(ctx, flaggedIDs); err != nil {
				return err
			}
		}
		for i, row := range rows {
			if string(row.Get("blocked")) == "true" || !matches(row.String("id")) {
				continue
			}
			id := fmt.Sprintf("installed-%d", i)
			if row.String("itemType") == "flagged-plugin" {
				add(id, row.String("name")+" @ "+row.String("marketplace")+" · flagged · removed", "", row, "plugin-open-flagged")
				continue
			}
			if row.String("itemType") == "mcp" {
				prefix := ""
				if string(row.Get("indented")) == "true" {
					prefix = "  └ "
				}
				add(id, prefix+row.String("name")+" · "+row.String("scope")+" · "+row.String("status"), "", row, "plugin-open-mcp")
				p.Rows[len(p.Rows)-1].Actions = append(p.Rows[len(p.Rows)-1].Actions, tui.LocalPanelAction{Key: "space", Name: "plugin-toggle-mcp", Label: "toggle"})
				continue
			}
			if string(row.Get("failed")) == "true" {
				add(id, row.String("name")+" @ "+row.String("marketplace")+" · "+row.String("scope")+" · failed to load", "", row, "plugin-open-failed")
				if panel.target != "" && (row.String("id") == panel.target || row.String("name") == strings.Split(panel.target, "@")[0]) {
					panel.selected, panel.level, panel.target, panel.action = row, "failed-detail", "", ""
					return s.refreshTerminalPluginPanel(ctx, state)
				}
				continue
			}
			status := "disabled"
			if string(row.Get("enabled")) == "true" {
				status = "enabled"
			}
			add(id, row.String("id")+" · "+row.String("scope")+" · "+status, row.String("description"), row, "plugin-open")
			if row.String("scope") == "user" || row.String("scope") == "project" || row.String("scope") == "local" || row.String("scope") == "builtin" {
				p.Rows[len(p.Rows)-1].Actions = append(p.Rows[len(p.Rows)-1].Actions, tui.LocalPanelAction{Key: "space", Name: "plugin-toggle", Label: "toggle"})
			}
			if panel.target != "" && (row.String("id") == panel.target || strings.Split(row.String("id"), "@")[0] == panel.target) {
				panel.selected = row
				panel.selection, err = s.process.CaptureTerminalPluginSelection(ctx, row.String("id"))
				if err != nil {
					return err
				}
				panel.level = "detail"
				panel.target = ""
				action := panel.action
				panel.action = ""
				if action != "" {
					if err = s.performTerminalPluginOperation(ctx, state, action); err != nil {
						p.Notice = err.Error()
					}
				}
				return s.refreshTerminalPluginPanel(ctx, state)
			}
		}
	case "marketplaces":
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "a", Name: "plugin-add-marketplace", Label: "add marketplace"}, tui.LocalPanelAction{Key: "u", Name: "plugin-market-update-all", Label: "update all"})
		for i, row := range array(snapshot.Get("marketplaces")) {
			if !matches(row.String("name")) {
				continue
			}
			row.Set("autoUpdateAvailable", snapshot.Get("autoUpdateAvailable"))
			add(fmt.Sprintf("market-%d", i), row.String("name"), row.String("installLocation"), row, "plugin-open-market")
			if panel.target != "" && row.String("name") == panel.target {
				panel.selected, panel.level, panel.target = row, "market-detail", ""
				action := panel.action
				panel.action = ""
				if action == "remove" {
					panel.level = "confirm-market-remove"
				}
				if action == "update" {
					if err = s.performTerminalPluginOperation(ctx, state, "marketplace-update"); err != nil {
						p.Notice = err.Error()
					}
				}
				return s.refreshTerminalPluginPanel(ctx, state)
			}
		}
		if panel.action == "list" {
			p.Description = "Configured marketplaces"
			panel.action = ""
		}
	case "errors":
		for i, row := range terminalPluginErrorRows(snapshot) {
			label := row.String("label")
			if row.String("scope") != "" {
				label += " · " + row.String("scope")
			}
			detail := row.String("message")
			if row.String("guidance") != "" {
				detail += "\n" + row.String("guidance")
			}
			add(fmt.Sprintf("error-%d", i), label, detail, row, "plugin-error")
		}
		p.HideDetails = false
	default:
		panel.available = array(snapshot.Get("available"))
		for i, row := range panel.available {
			if panel.marketplace != "" && row.String("marketplaceName") != panel.marketplace || !matches(row.String("name")+" "+row.String("description")) {
				continue
			}
			row.Set("id", row.Get("pluginId"))
			add(fmt.Sprintf("available-%d", i), row.String("id")+" · "+row.String("description"), row.String("description"), row, "plugin-open")
			p.Rows[len(p.Rows)-1].Actions = append(p.Rows[len(p.Rows)-1].Actions, tui.LocalPanelAction{Key: "space", Name: "plugin-select", Label: "toggle install"})
			if _, selected := panel.selectedForInstall[row.String("id")]; selected {
				p.Rows[len(p.Rows)-1].Label = "[selected] " + p.Rows[len(p.Rows)-1].Label
				p.Rows[len(p.Rows)-1].Actions[0] = tui.LocalPanelAction{Key: "enter", Name: "plugin-install-selected", Label: "install selected"}
			}
			if panel.target != "" && (row.String("id") == panel.target || row.String("name") == panel.target) {
				panel.selected, panel.level, panel.target = row, "detail", ""
				return s.refreshTerminalPluginPanel(ctx, state)
			}
		}
	}
	if panel.tab == "discover" && len(panel.selectedForInstall) > 0 {
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "i", Name: "plugin-install-selected", Label: "install selected"})
	}
	return nil
}
func (s *Session) performTerminalPluginOperation(ctx context.Context, state *terminalLocalPanelState, action string) error {
	panel := state.pluginPanel
	row := panel.selected
	if message := terminalPluginOperationGuard(row.String("scope"), action); message != "" {
		return errors.New(message)
	}
	if panel.selection == nil && (action == "enable" || action == "disable" || action == "update" || action == "uninstall") {
		selection, err := s.process.CaptureTerminalPluginSelection(ctx, row.String("id"))
		if err != nil {
			return err
		}
		panel.selection = selection
	}
	command := plugincli.Command{Action: action, Argument: row.String("id")}
	switch action {
	case "install":
		command.Scope = panel.scope
	case "update", "uninstall":
		command.Scope = row.String("scope")
	case "marketplace-update", "marketplace-remove", "marketplace-auto-update":
		command.Argument = row.String("name")
	}
	if action == "uninstall" {
		if string(row.Get("projectEnabled")) == "true" {
			panel.level = "confirm-project-uninstall"
			return nil
		}
		data, err := s.process.TerminalPluginDataSize(ctx, row.String("id"))
		if err != nil {
			return err
		}
		if string(data.Get("size")) != "null" {
			panel.selected.Set("dataSize", data.Get("size"))
			panel.level = "confirm-data-cleanup"
			return nil
		}
	}
	result, err := s.process.TerminalPluginOperation(ctx, command)
	if err != nil {
		if action == "update" {
			return fmt.Errorf("Failed to update: %w", err)
		}
		return err
	}
	if action == "update" && string(result.Get("alreadyUpToDate")) == "true" {
		state.props.Notice = row.String("name") + " is already at the latest version (" + result.String("newVersion") + ")."
		panel.level = ""
		return nil
	}
	state.props.Notice = result.String("message") + " Run /reload-plugins to apply."
	if action == "update" {
		state.props.Notice = "✓ Updated " + row.String("name") + ". Run /reload-plugins to apply."
	}
	postEnable := false
	var postConfiguration protocol.JSONObject
	if action == "enable" || action == "disable" || action == "update" || action == "uninstall" {
		postConfiguration, err = s.process.TerminalSelectedPluginPostConfiguration(ctx, panel.selection)
		if err != nil {
			return err
		}
		postEnable = string(postConfiguration.Get("enabled")) == "true"
	}
	if action == "install" || postEnable {
		config := postConfiguration
		if action == "install" {
			config, err = s.process.TerminalPluginConfiguration(ctx, row.String("id"), false)
			if err != nil {
				return err
			}
		}
		panel.steps = array(config.Get("steps"))
		panel.step = 0
		if len(panel.steps) > 0 {
			panel.postEnableConfiguration = postEnable
			panel.level = "configuration"
			return nil
		}
		if postEnable {
			state.props.Notice = terminalPluginPostEnableMessage(row.String("name"), "skipped", "")
		}
	}
	panel.level = ""
	return nil
}
func terminalPluginFinalValues(step protocol.JSONObject, fields map[string]string) protocol.JSONObject {
	return terminalPluginBoundValues(step, fields, nil)
}
func (s *Session) applyTerminalPluginPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult) error {
	panel := state.pluginPanel
	if panel == nil {
		return errors.New("Plugin panel is no longer active")
	}
	row, exists := state.rows[result.Row]
	switch result.Action {
	case "plugin-select":
		if !exists || row.String("id") == "" {
			return errors.New("Plugin row is no longer available")
		}
		if panel.selectedForInstall == nil {
			panel.selectedForInstall = map[string]protocol.JSONObject{}
		}
		if _, selected := panel.selectedForInstall[row.String("id")]; selected {
			delete(panel.selectedForInstall, row.String("id"))
		} else {
			panel.selectedForInstall[row.String("id")] = row
		}
	case "plugin-install-selected":
		return s.installTerminalPluginSelection(ctx, state)
	case "plugin-toggle":
		if !exists {
			return errors.New("Plugin row is no longer available")
		}
		operation := "enable"
		if string(row.Get("enabled")) == "true" {
			operation = "disable"
		}
		if _, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: operation, Argument: row.String("id")}); err != nil {
			return err
		}
		if panel.pendingToggles == nil {
			panel.pendingToggles = map[string]bool{}
		}
		if _, pending := panel.pendingToggles[row.String("id")]; pending {
			delete(panel.pendingToggles, row.String("id"))
		} else {
			panel.pendingToggles[row.String("id")] = true
		}
		state.props.Notice = "Run /reload-plugins to apply plugin changes."
	case "plugin-tab":
		tabs := []string{"discover", "installed", "marketplaces", "errors"}
		for i, tab := range tabs {
			if tab == panel.tab {
				panel.tab = tabs[(i+1)%len(tabs)]
				break
			}
		}
		panel.level = ""
		panel.search = ""
		panel.marketplace = ""
	case "plugin-scope":
		scopes := []string{"user", "project", "local"}
		for i, scope := range scopes {
			if scope == panel.scope {
				panel.scope = scopes[(i+1)%len(scopes)]
				break
			}
		}
	case "plugin-back":
		state.props.Notice = ""
		oldLevel := panel.level
		if oldLevel == "configuration" && panel.postEnableConfiguration {
			state.props.Notice = terminalPluginPostEnableMessage(panel.selected.String("name"), "skipped", "")
			panel.postEnableConfiguration = false
		}
		panel.level = ""
		if oldLevel == "confirm-project-uninstall" || oldLevel == "confirm-data-cleanup" {
			panel.level = "detail"
		}
		if panel.configurationReturnsToDetail {
			panel.level = "detail"
			panel.configurationReturnsToDetail = false
		}
		panel.steps = nil
		panel.step = 0
	case "plugin-search":
		panel.level = "search"
	case "plugin-add-marketplace":
		panel.level = "add-marketplace"
		panel.target = ""
	case "plugin-open-failed":
		state.props.Notice = ""
		if !exists {
			return errors.New("Failed plugin is no longer available")
		}
		panel.selected, panel.level = row, "failed-detail"
		panel.lastSelected = result.Row
	case "plugin-open-flagged":
		state.props.Notice = ""
		if !exists {
			return errors.New("Flagged plugin is no longer available")
		}
		panel.selected, panel.level = row, "flagged-detail"
		panel.lastSelected = result.Row
	case "plugin-dismiss-flagged":
		if err := s.process.RemoveTerminalFlaggedPluginJSON(ctx, panel.selected.Get("id")); err != nil {
			return err
		}
		panel.level = ""
	case "plugin-open-mcp":
		state.props.Notice = ""
		if !exists {
			return errors.New("MCP server is no longer available")
		}
		state.mcpMenu = &terminalMCPMenuState{server: obj(row.Get("client"))}
		panel.level = "mcp-detail"
		panel.lastSelected = result.Row
	case "plugin-toggle-mcp":
		if !exists {
			return errors.New("MCP server is no longer available")
		}
		client := obj(row.Get("client"))
		enabled := client.String("status") == "disabled"
		return s.process.TerminalMCPControl(ctx, client.String("name"), &enabled)
	case "plugin-mark-update":
		if err := s.process.CheckTerminalPluginUpdate(ctx, panel.selected.String("id")); err != nil {
			return err
		}
		if panel.pendingUpdates == nil {
			panel.pendingUpdates = map[string]bool{}
		}
		panel.pendingUpdates[panel.selected.String("id")] = !panel.pendingUpdates[panel.selected.String("id")]
	case "plugin-remove-failed":
		if panel.selected.String("scope") == "managed" {
			return errors.New("Managed by your organization — contact your admin")
		}
		if err := s.process.RemoveTerminalFailedPlugin(ctx, panel.selected.String("id"), panel.selected.String("scope")); err != nil {
			return err
		}
		panel.level = ""
		state.props.Notice = "Run /reload-plugins to apply plugin changes."
	case "plugin-open", "plugin-open-market":
		state.props.Notice = ""
		if !exists {
			return errors.New("Plugin row is no longer available")
		}
		panel.selected = row
		panel.selection = nil
		if result.Action == "plugin-open" && panel.tab == "installed" {
			selection, err := s.process.CaptureTerminalPluginSelection(ctx, row.String("id"))
			if err != nil {
				return err
			}
			panel.selection = selection
		}
		panel.lastSelected = result.Row
		panel.level = "detail"
		if result.Action == "plugin-open-market" {
			panel.level = "market-detail"
		}
	case "plugin-browse":
		panel.marketplace = panel.selected.String("name")
		panel.tab, panel.level = "discover", ""
	case "plugin-error":
		if !exists {
			return errors.New("Plugin error row is no longer available")
		}
		action := obj(row.Get("action"))
		switch action.String("kind") {
		case "remove-extra-marketplace":
			result, err := s.process.RemoveTerminalPluginExtraMarketplace(ctx, action.String("name"))
			if err != nil {
				return err
			}
			state.props.Notice = result.String("message")
		case "remove-installed-marketplace":
			_, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "marketplace-remove", Argument: action.String("name")})
			if err != nil {
				return err
			}
			state.props.Notice = "✔ Removed marketplace \"" + action.String("name") + "\""
		case "navigate":
			view := obj(action.Get("viewState"))
			panel.tab = action.String("tab")
			panel.target = view.String("targetPlugin")
			if panel.tab == "marketplaces" {
				panel.target = view.String("targetMarketplace")
			}
			panel.action = view.String("action")
			panel.level = ""
		case "managed-only", "none":
			return nil
		}
		state.props.HideDetails = false
		return nil
	case "plugin-install", "plugin-enable", "plugin-disable", "plugin-update", "plugin-uninstall":
		if !exists {
			return errors.New("Plugin row is no longer available")
		}
		return s.performTerminalPluginOperation(ctx, state, strings.TrimPrefix(result.Action, "plugin-"))
	case "plugin-install-user", "plugin-install-project", "plugin-install-local":
		if !exists {
			return errors.New("Plugin row is no longer available")
		}
		panel.scope = strings.TrimPrefix(result.Action, "plugin-install-")
		return s.performTerminalPluginOperation(ctx, state, "install")
	case "plugin-homepage", "plugin-github", "plugin-repository":
		if !exists {
			return errors.New("Plugin row is no longer available")
		}
		entry := obj(panel.selected.Get("entry"))
		if panel.selected.Get("enabled") != nil {
			entry = obj(panel.selected.Get("manifest"))
		}
		address := entry.String("homepage")
		if result.Action == "plugin-repository" {
			address = entry.String("repository")
		} else if result.Action == "plugin-github" {
			address = "https://github.com/" + obj(entry.Get("source")).String("repo")
		}
		_, err := s.process.OpenTerminalPluginLink(ctx, address)
		return err
	case "plugin-configure-mcpb":
		config, err := s.process.TerminalPluginMCPBConfiguration(ctx, panel.selected.String("id"), nil)
		if err != nil {
			return err
		}
		panel.steps = array(config.Get("steps"))
		panel.step = 0
		panel.configurationReturnsToDetail = true
		panel.level = "configuration"
	case "plugin-configure":
		config, err := s.process.TerminalSelectedPluginConfiguration(ctx, panel.selection, true)
		if err != nil {
			return err
		}
		panel.steps = array(config.Get("steps"))
		panel.step = 0
		panel.configurationReturnsToDetail = true
		panel.level = "configuration"
	case "plugin-market-update":
		return s.performTerminalPluginOperation(ctx, state, "marketplace-update")
	case "plugin-market-update-all":
		_, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "marketplace-update"})
		return err
	case "plugin-market-remove":
		panel.level = "confirm-market-remove"
	case "plugin-market-remove-confirmed":
		return s.performTerminalPluginOperation(ctx, state, "marketplace-remove")
	case "plugin-market-auto-update":
		_, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "marketplace-auto-update", Argument: panel.selected.String("name"), JSON: string(panel.selected.Get("autoUpdate")) != "true"})
		if err != nil {
			return err
		}
		panel.level = ""
		return nil
	case "plugin-disable-local":
		value, err := s.process.DisableTerminalPluginLocally(ctx, panel.selected.String("id"))
		if err != nil {
			return err
		}
		panel.level = ""
		state.props.Notice = value.String("message")
		return nil
	case "plugin-remove-data", "plugin-keep-data":
		_, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "uninstall", Argument: panel.selected.String("id"), Scope: panel.selected.String("scope"), KeepData: result.Action == "plugin-keep-data"})
		if err != nil {
			return err
		}
		panel.level = ""
		return nil
	case "save":
		switch state.form {
		case "plugin-options":
			if panel.step >= len(panel.steps) {
				return errors.New("Plugin configuration no longer active")
			}
			step := panel.steps[panel.step]
			values := terminalPluginFinalValuesExact(step, result.Fields, result.FieldsJSON)
			var saveError error
			if step.String("kind") == "mcpb" {
				_, saveError = s.process.TerminalPluginMCPBConfiguration(ctx, panel.selected.String("id"), &values)
			} else if panel.selection != nil {
				saveError = s.process.SaveTerminalSelectedPluginConfiguration(ctx, panel.selection, step.String("server"), values)
			} else {
				saveError = s.process.SaveTerminalPluginConfiguration(ctx, panel.selected.String("id"), step.String("server"), values)
			}
			if saveError != nil {
				if !panel.postEnableConfiguration {
					return saveError
				}
				state.props.Notice = terminalPluginPostEnableMessage(panel.selected.String("name"), "error", saveError.Error())
				panel.level, panel.steps, panel.postEnableConfiguration = "", nil, false
				return nil
			}
			panel.step++
			if panel.postEnableConfiguration && panel.step >= len(panel.steps) {
				state.props.Notice = terminalPluginPostEnableMessage(panel.selected.String("name"), "configured", "")
				panel.postEnableConfiguration = false
			}
			if panel.configurationReturnsToDetail {
				state.props.Notice = "Configuration saved. Run /reload-plugins for changes to take effect."
			}
		case "plugin-input":
			value := result.Fields["value"]
			switch panel.level {
			case "search":
				panel.search = value
				panel.level = ""
			case "add-marketplace":
				result, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "marketplace-add", Argument: value, Scope: panel.scope})
				if err != nil {
					return err
				}
				state.props.Notice = result.String("message")
				panel.level = ""
				panel.tab = "marketplaces"
			case "validate":
				result, err := s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "validate", Argument: value})
				if err != nil {
					return err
				}
				state.props.Notice = result.String("message")
				panel.level = ""
			}
		default:
			return errors.New("Plugin form is no longer available")
		}
	default:
		return errors.New("Unknown plugin panel action")
	}
	return nil
}

func terminalPluginFinalValuesExact(step protocol.JSONObject, fields map[string]string, encoded map[string]json.RawMessage) protocol.JSONObject {
	return terminalPluginBoundValues(step, fields, encoded)
}
