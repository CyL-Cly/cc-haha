package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"sort"
	"strings"
	"sync"
)

type terminalLocalPanelState struct {
	mu                    sync.Mutex
	props                 tui.LocalPanel
	rows                  map[string]protocol.JSONObject
	form, selected        string
	configSnapshot        *protocol.JSONObject
	configDirty           map[string]bool
	configExternalTouched bool
	workflowPanel         *terminalWorkflowPanelState
	pluginPanel           *terminalPluginPanelState
	directoryPanel        *terminalDirectoryPanelState
	workspacePanel        *terminalWorkspacePanelState
	usagePanel            *terminalUsagePanelState
	themePicker           *terminalThemePickerState
	mcpMenu               *terminalMCPMenuState
	mcpDiagnostics        []protocol.JSONObject
	resumePanel           *terminalResumePanelState
	btwPanel              *terminalBtwPanelState
	teamsPanel            *terminalTeamsPanelState
	helpPanel             *terminalHelpPanelState
}

func terminalPanelCommands() []tui.Completion {
	commands := []tui.Completion{
		{Name: "help", Description: "Show help and available commands", Kind: "local-jsx"},
		{Name: "clear", Description: "Clear conversation history and free up context", Kind: "local"},
		{Name: "reset", Description: "Clear conversation history and free up context", Kind: "local"},
		{Name: "new", Description: "Clear conversation history and free up context", Kind: "local"},
		{Name: "permissions", Description: "Manage allow & deny tool permission rules", Kind: "local-jsx"},
		{Name: "allowed-tools", Description: "Manage allow & deny tool permission rules", Kind: "local-jsx"},
		{Name: "tasks", Description: "List and manage background tasks", Kind: "local-jsx"},
		{Name: "bashes", Description: "List and manage background tasks", Kind: "local-jsx"},
		{Name: "skills", Description: "List available skills", Kind: "local-jsx"},
		{Name: "agents", Description: "Manage agent configurations", Kind: "local-jsx"},
		{Name: "mcp", Description: "Manage MCP servers", Kind: "local-jsx", ArgumentHint: "[enable|disable [server-name]]"},
		{Name: "memory", Description: "Edit Claude memory files", Kind: "local-jsx"},
		{Name: "hooks", Description: "View hook configurations for tool events", Kind: "local-jsx"},
		{Name: "status", Description: "Show Claude Code status including version, model, account, API connectivity, and tool statuses", Kind: "local-jsx"},
		{Name: "config", Description: "Open config panel", Kind: "local-jsx"},
		{Name: "settings", Description: "Open config panel", Kind: "local-jsx"},
		{Name: "theme", Description: "Change the theme", Kind: "local-jsx"},
		{Name: "output-style", Description: "Deprecated: use /config to change output style", Kind: "local-jsx", Hidden: true},
		{Name: "plugin", Description: "Manage Claude Code plugins", Kind: "local-jsx"},
		{Name: "plugins", Description: "Manage Claude Code plugins", Kind: "local-jsx"},
		{Name: "marketplace", Description: "Manage Claude Code plugins", Kind: "local-jsx"},
		{Name: "reload-plugins", Description: "Activate pending plugin changes in the current session", Kind: "local"},
		{Name: "add-dir", Description: "Add a new working directory", Kind: "local-jsx", ArgumentHint: "<path>"},
	}
	commands = append(commands, terminalContextStatsCommands()...)
	commands = append(commands, terminalBranchDiffCommands()...)
	commands = append(commands, terminalResumeCommands()...)
	commands = append(commands, terminalMobileCommands()...)
	commands = append(commands, terminalBuddyCommands()...)
	commands = append(commands, terminalBtwCommands()...)
	return append(commands, terminalVimCommands()...)
}
func terminalPanelKind(name string) string {
	switch name {
	case "allowed-tools":
		return "permissions"
	case "bashes":
		return "tasks"
	case "settings":
		return "config"
	case "plugins", "marketplace":
		return "plugin"
	case "help", "permissions", "tasks", "skills", "agents", "mcp", "memory", "status", "usage", "output-style", "fast", "hooks", "config", "theme", "workflows", "plugin", "reload-plugins", "add-dir":
		return name
	}
	return ""
}
func (s *Session) InvokeTerminalCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	emit = terminalTaskListCommandEmit(ctx, name, emit)
	if handled, err := s.invokeTerminalBtwCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalResumeCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalBuddyCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalMobileCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalBranchDiffCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalClearCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalVimCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalContextStatsCommand(ctx, name, args, emit); handled {
		return true, err
	}
	if handled, err := s.invokeTerminalConversationCommand(ctx, name, args, emit); handled {
		return true, err
	}
	kind := terminalPanelKind(name)
	if kind == "" {
		return false, nil
	}
	if kind == "usage" && !s.process.NativeUsageCommandEnabled() {
		return false, nil
	}
	if kind == "reload-plugins" {
		value, err := s.process.TerminalReloadPlugins(ctx, args)
		if err != nil {
			return true, err
		}
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalCommandOutput, Text: value.String("message"), Background: true})
		}
		return true, nil
	}
	if kind == "workflows" && !s.process.NativeWorkflowsCommandEnabled() {
		return false, nil
	}
	if kind == "output-style" {
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.Notice, Text: "/output-style has been deprecated. Use /config to change your output style, or set it in your settings file. Changes take effect on the next session."})
		}
		return true, nil
	}
	if kind == "fast" {
		if !s.process.NativeFastCommandEnabled() {
			return false, nil
		}
		argument := strings.ToLower(strings.TrimSpace(args))
		if argument == "on" || argument == "off" {
			enabled := argument == "on"
			value, err := s.process.TerminalFastMode(ctx, &enabled)
			if err != nil {
				if emit != nil {
					return true, emit(tui.Event{Kind: tui.Notice, Text: "Fast mode unavailable: " + err.Error()})
				}
				return true, err
			}
			text := "Fast mode OFF"
			if string(value.Get("enabled")) == "true" {
				text = "Fast mode ON"
			}
			if emit != nil {
				return true, emit(tui.Event{Kind: tui.Notice, Text: text})
			}
			return true, nil
		}
	}
	if kind == "mcp" {
		parts := strings.Fields(args)
		if len(parts) > 0 && (parts[0] == "enable" || parts[0] == "disable" || parts[0] == "reconnect") {
			value, err := s.process.TerminalPanelSnapshot(ctx, "mcp")
			if err != nil {
				return true, err
			}
			var rows []protocol.JSONObject
			_ = json.Unmarshal(value.Get("servers"), &rows)
			target := strings.Join(parts[1:], " ")
			if target == "" {
				target = "all"
			}
			matched := 0
			for _, row := range rows {
				server := row.String("name")
				if server == "ide" || target != "all" && server != target {
					continue
				}
				var enabled *bool
				if parts[0] != "reconnect" {
					flag := parts[0] == "enable"
					enabled = &flag
					if (row.String("status") != "disabled") == flag {
						continue
					}
				}
				if err = s.process.TerminalMCPControl(ctx, server, enabled); err != nil {
					return true, err
				}
				matched++
			}
			notice := fmt.Sprintf("%s %d MCP server(s)", strings.Title(parts[0]), matched)
			if matched == 0 && target != "all" {
				notice = fmt.Sprintf("MCP server %q not found or already %sd", target, parts[0])
			}
			if emit != nil {
				return true, emit(tui.Event{Kind: tui.Notice, Text: notice})
			}
			return true, nil
		}
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: kind}, rows: map[string]protocol.JSONObject{}}
	initializeTerminalSettingsTabs(state, kind != "config")
	if kind == "help" {
		state.helpPanel = &terminalHelpPanelState{args: args, done: make(chan struct{})}
	}
	if kind == "plugin" {
		state.pluginPanel = parseTerminalPluginArgs(args)
		if handled, err := s.initializeTerminalPluginPanel(ctx, state, emit); handled {
			return true, err
		}
	}
	if kind == "add-dir" {
		state.directoryPanel = &terminalDirectoryPanelState{}
		if argument := strings.TrimSpace(args); argument != "" {
			validation, err := s.process.ValidateTerminalDirectory(ctx, argument)
			if err != nil {
				return true, err
			}
			if validation.String("resultType") != "success" {
				if emit != nil {
					return true, emit(tui.Event{Kind: tui.Notice, Text: terminalDirectoryHelp(validation)})
				}
				return true, nil
			}
			state.directoryPanel.path = validation.String("absolutePath")
		}
	}
	if err := s.refreshTerminalPanel(ctx, state); err != nil {
		return true, err
	}
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	s.closeTerminalBtwPanel(previous)
	s.closeTerminalTeamsPanel(previous)
	s.closeTerminalHelpPanel(previous)
	s.mu.Lock()
	s.terminalPanel = state
	s.mu.Unlock()
	if emit != nil {
		props := terminalPanelProjection(state.props)
		if err := emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props}); err != nil {
			return true, err
		}
	}
	if kind == "usage" {
		return true, s.startTerminalUsagePanel(ctx, state, emit)
	}
	if kind == "help" {
		return true, state.helpPanel.wait(ctx)
	}
	return true, nil
}
func (s *Session) refreshTerminalPanel(ctx context.Context, state *terminalLocalPanelState) error {
	if state.props.Kind == "usage" {
		return s.refreshTerminalUsagePanel(ctx, state)
	}
	if state.props.Kind == "stats" {
		return s.refreshTerminalContextStatsPanel(ctx, state)
	}
	p := &state.props
	p.Rows, p.Fields, p.Actions = nil, nil, nil
	p.DismissAction = ""
	p.RawInputKeys = nil
	p.RowDescriptions, p.VisibleRows = false, 0
	state.rows, state.form = map[string]protocol.JSONObject{}, ""
	add := func(row protocol.JSONObject, id, label, detail string, actions ...tui.LocalPanelAction) {
		state.rows[id] = row.Clone()
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: id, Label: label, Detail: detail, Actions: actions})
	}
	p.Actions = []tui.LocalPanelAction{{Key: "r", Name: "refresh", Label: "refresh"}}
	switch p.Kind {
	case "copy", "export", "tag":
		return s.refreshTerminalConversationPanel(ctx, state)
	case "workflows":
		return s.refreshTerminalWorkflowPanel(ctx, state)
	case "plugin":
		return s.refreshTerminalPluginPanel(ctx, state)
	case "add-dir":
		return s.refreshTerminalDirectoryPanel(state)
	case "theme":
		return s.openTerminalThemePicker(ctx, state)
	case "config":
		return s.refreshTerminalConfigPanel(ctx, state)
	case "hooks":
		p.Title, p.Description = "Hooks", "Hook configurations for tool events. Edit settings.json or ask Claude to add or change hooks."
		rows, err := s.process.TerminalHookConfigurations(ctx)
		if err != nil {
			return err
		}
		for i, row := range rows {
			hook := obj(row.Get("hook"))
			detail := hook.String("command")
			if detail == "" {
				detail = hook.String("prompt")
			}
			if detail == "" {
				detail = hook.String("url")
			}
			add(row, fmt.Sprintf("hook-%d", i), row.String("event")+" · "+row.String("source")+" · "+hook.String("type"), row.String("matcher")+"\n"+detail+"\n"+string(raw(hook)))
		}
	case "memory":
		p.Title, p.Description = "Memory", "Edit user and project memory files"
		files, err := s.process.TerminalMemoryFiles(ctx)
		if err != nil {
			return err
		}
		for _, row := range files {
			add(row, row.String("path"), row.String("type")+" · "+row.String("path"), row.String("content"), tui.LocalPanelAction{Key: "e", Name: "edit-memory", Label: "edit"})
		}
	case "status":
		p.Title = "Claude Code status"
		value, err := s.process.TerminalStatus(ctx)
		if err != nil {
			return err
		}
		var properties []struct {
			Label string
			Value json.RawMessage
		}
		_ = json.Unmarshal(value.Get("properties"), &properties)
		for index, property := range properties {
			label := configLabel(property.Value)
			if len(property.Value) > 0 && property.Value[0] == '[' {
				var values []string
				_ = json.Unmarshal(property.Value, &values)
				label = strings.Join(values, ", ")
			}
			if property.Label != "" {
				label = property.Label + ": " + label
			}
			add(protocol.NewJSONObject(), fmt.Sprintf("status-%d", index), label, "")
		}
		var diagnostics []string
		_ = json.Unmarshal(value.Get("diagnostics"), &diagnostics)
		p.Description = strings.Join(diagnostics, "\n")
		p.HideDetails = true
	case "help":
		return s.refreshTerminalHelpPanel(ctx, state)
	case "skills":
		p.Title, p.Description = "Skills", "Available skills from the current project, user, plugins and MCP"
		value, err := s.process.TerminalPanelSnapshot(ctx, "skills")
		if err != nil {
			return err
		}
		var rows []protocol.JSONObject
		_ = json.Unmarshal(value.Get("skills"), &rows)
		for _, row := range rows {
			name := row.String("userFacingName")
			if name == "" {
				name = row.String("name")
			}
			add(row, name, "/"+name+" · "+row.String("source")+" · "+row.String("description"), row.String("skillRoot"))
		}
	case "permissions":
		if state.workspacePanel != nil {
			return s.refreshTerminalPermissionWorkspace(ctx, state)
		}
		p.Title, p.Description = "Permissions", "Allow, deny and ask rules with their actual source"
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "a", Name: "add", Label: "add rule"})
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "w", Name: "workspace-open", Label: "workspace"})
		value, err := s.process.TerminalPanelSnapshot(ctx, "permissions")
		if err != nil {
			return err
		}
		for _, behavior := range []string{"allow", "deny", "ask"} {
			key := map[string]string{"allow": "alwaysAllowRules", "deny": "alwaysDenyRules", "ask": "alwaysAskRules"}[behavior]
			collection := obj(value.Get(key))
			for _, property := range collection.OwnProperties() {
				var source string
				_ = json.Unmarshal(property.NameJSON, &source)
				var rules []string
				_ = json.Unmarshal(property.Value, &rules)
				for _, rule := range rules {
					id := behavior + "\x00" + source + "\x00" + rule
					row := obj(raw(map[string]any{"behavior": behavior, "source": source, "rule": rule}))
					actions := []tui.LocalPanelAction{}
					if source != "policySettings" && source != "cliArg" {
						actions = append(actions, tui.LocalPanelAction{Key: "x", Name: "remove", Label: "remove rule"})
					}
					add(row, id, behavior+" · "+rule+" · "+source, rule, actions...)
				}
			}
		}
	case "tasks":
		p.Title, p.Description = "Background tasks", "Actual running and completed tasks in this session"
		value, err := s.process.TerminalPanelSnapshot(ctx, "tasks")
		if err != nil {
			return err
		}
		var rows []protocol.JSONObject
		_ = json.Unmarshal(value.Get("tasks"), &rows)
		for _, row := range rows {
			actions := []tui.LocalPanelAction{{Key: "o", Name: "output", Label: "view output"}}
			if row.String("status") == "running" {
				actions = append(actions, tui.LocalPanelAction{Key: "x", Name: "stop", Label: "stop"})
				if row.String("type") == "in_process_teammate" {
					actions = append(actions, tui.LocalPanelAction{Key: "f", Name: "foreground", Label: "foreground"})
				}
			}
			add(row, row.String("id"), row.String("description")+" · "+row.String("status"), row.String("type")+" · "+row.String("id"), actions...)
		}
	case "agents":
		p.Title, p.Description = "Agents", "Configured agents used by the current session"
		p.Actions = append(p.Actions, tui.LocalPanelAction{Key: "a", Name: "create", Label: "create agent"})
		value, err := s.process.TerminalAgents(ctx, "GET", "", nil)
		if err != nil {
			return err
		}
		var rows []protocol.JSONObject
		_ = json.Unmarshal(value.Get("allAgents"), &rows)
		for _, row := range rows {
			actions := []tui.LocalPanelAction{}
			if string(row.Get("editable")) == "true" {
				actions = append(actions, tui.LocalPanelAction{Key: "e", Name: "edit", Label: "edit"}, tui.LocalPanelAction{Key: "x", Name: "delete", Label: "delete"})
			}
			id := row.String("source") + ":" + row.String("agentType")
			add(row, id, row.String("agentType")+" · "+row.String("source")+" · "+row.String("modelDisplay"), row.String("description")+"\n"+row.String("systemPrompt"), actions...)
		}
	case "mcp":
		return s.refreshTerminalMCPPanel(ctx, state)
	case "fast":
		p.Title, p.Description = "Fast mode", "High-speed mode for Opus 4.8. Billed as extra usage at a premium rate."
		value, err := s.process.TerminalFastMode(ctx, nil)
		if err != nil {
			return err
		}
		p.Notice = value.String("unavailableReason")
		add(value, "fast", "Fast mode · "+map[bool]string{true: "ON", false: "OFF"}[string(value.Get("enabled")) == "true"], p.Description, tui.LocalPanelAction{Key: "t", Name: "toggle", Label: "toggle"})
	default:
		return errors.New("Unknown local panel")
	}
	if p.Kind != "tasks" {
		sort.SliceStable(p.Rows, func(i, j int) bool { return p.Rows[i].Label < p.Rows[j].Label })
	}
	return nil
}
