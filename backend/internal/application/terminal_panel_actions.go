package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"strings"
)

func (s *Session) ApplyLocalPanel(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) error {
	if handled, err := s.applyTerminalTeamsPanel(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalBtwPanel(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalMCPAuthentication(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.refreshTerminalPluginInstallation(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalSettingsTab(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalUsagePanel(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalContextStatsPanel(ctx, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalConversationPanel(ctx, result, emit); handled {
		return err
	}
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID {
		return errors.New("Local panel is no longer active")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	s.mu.Lock()
	active := s.terminalPanel == state
	s.mu.Unlock()
	if !active {
		return errors.New("Local panel is no longer active")
	}
	if handled, err := s.applyTerminalMobilePanel(ctx, state, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalHelpPanel(ctx, state, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalBuddyPanel(ctx, state, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalBranchDiffPanel(ctx, state, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalResumePanel(ctx, state, result, emit); handled {
		return err
	}
	if handled, err := s.applyTerminalThemePicker(ctx, state, result, emit); handled {
		return err
	}
	if result.Action == "close" {
		if state.props.Kind == "add-dir" {
			if _, err := s.applyTerminalDirectoryPanel(ctx, state, tui.LocalPanelResult{Action: "directory-cancel"}, emit); err != nil {
				return err
			}
		}
		if state.props.Kind == "config" && state.configSnapshot != nil && len(state.configDirty) > 0 {
			keys := []string{}
			for key := range state.configDirty {
				keys = append(keys, key)
			}
			if state.configExternalTouched {
				keys = append(keys, "showExternalIncludesDialog")
			}
			if err := s.process.RestoreTerminalConfig(ctx, *state.configSnapshot, keys); err != nil {
				return err
			}
			if err := s.emitTerminalConfig(ctx, emit); err != nil {
				return err
			}
		}
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		return nil
	}
	row, exists := state.rows[result.Row]
	var completionNotices []tui.Event
	completionEmit := func(event tui.Event) error {
		if event.Kind == tui.Notice {
			completionNotices = append(completionNotices, event)
			return nil
		}
		if emit != nil {
			return emit(event)
		}
		return nil
	}
	closeWithNotices := func() error {
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			if err := emit(tui.Event{Kind: tui.LocalPanelClosed}); err != nil {
				return err
			}
			for _, event := range completionNotices {
				if err := emit(event); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if state.props.Kind == "permissions" && (strings.HasPrefix(result.Action, "workspace-") || result.Action == "save" && state.workspacePanel != nil) {
		err := s.applyTerminalPermissionWorkspace(ctx, state, result)
		if err != nil {
			state.props.Notice = err.Error()
		}
		if refreshErr := s.refreshTerminalPanel(ctx, state); refreshErr != nil {
			err = refreshErr
			state.props.Notice = err.Error()
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if state.props.Kind == "add-dir" {
		closed, err := s.applyTerminalDirectoryPanel(ctx, state, result, completionEmit)
		if err != nil {
			state.props.Notice = err.Error()
		} else if closed {
			return closeWithNotices()
		} else {
			err = s.refreshTerminalPanel(ctx, state)
			if err != nil {
				state.props.Notice = err.Error()
			}
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if state.props.Kind == "plugin" && (strings.HasPrefix(result.Action, "plugin-") || result.Action == "save") {
		err := s.applyTerminalPluginPanel(ctx, state, result)
		if err != nil {
			state.props.Notice = err.Error()
		} else {
			err = s.refreshTerminalPanel(ctx, state)
			if err != nil {
				state.props.Notice = err.Error()
			}
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if strings.HasPrefix(result.Action, "mcp-") && (state.props.Kind == "mcp" || state.props.Kind == "plugin") {
		err := s.applyTerminalMCPMenu(ctx, state, result)
		if err != nil {
			state.props.Notice = err.Error()
		} else if refreshErr := s.refreshTerminalPanel(ctx, state); refreshErr != nil {
			err = refreshErr
			state.props.Notice = err.Error()
		}
		if emit != nil {
			props := state.props
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if strings.HasPrefix(result.Action, "workflow-") {
		closed, err := s.applyTerminalWorkflowPanel(ctx, state, result, completionEmit)
		if err != nil {
			state.props.Notice = err.Error()
		} else if closed {
			return closeWithNotices()
		} else {
			state.props.Notice = ""
			err = s.refreshTerminalPanel(ctx, state)
			if err != nil {
				state.props.Notice = err.Error()
			}
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if state.props.Kind == "config" && (result.Action == "config-external-select" || result.Action == "config-external-disable") {
		err := s.applyTerminalExternalIncludes(ctx, state, result)
		if err == nil {
			err = s.refreshTerminalPanel(ctx, state)
		}
		if err != nil {
			state.props.Notice = err.Error()
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if state.props.Kind == "config" && (result.Action == "config-back" || result.Action == "config-channel-select") {
		err := s.applyTerminalConfigSubmenu(ctx, state, result)
		if err == nil {
			err = s.emitTerminalConfig(ctx, emit)
		}
		if err == nil {
			err = s.refreshTerminalPanel(ctx, state)
		}
		if err != nil {
			state.props.Notice = err.Error()
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
		}
		return err
	}
	if result.Action != "refresh" && result.Action != "add" && result.Action != "create" && result.Action != "save" && result.Action != "commit-config" && !exists {
		return errors.New("Panel row is no longer available")
	}
	var err error
	form := false
	switch result.Action {
	case "select-output-style":
		if state.props.Kind != "config" || state.form != "config-output-style" {
			return errors.New("Invalid panel action")
		}
		err = s.process.SetTerminalConfigSetting(ctx, "outputStyle", raw(result.Row))
		if err == nil {
			if state.configDirty == nil {
				state.configDirty = map[string]bool{}
			}
			state.configDirty["outputStyle"] = true
			err = s.emitTerminalConfig(ctx, emit)
		}
	case "commit-config", "select-theme":
		if result.Action == "select-theme" {
			if state.props.Kind != "theme" {
				return errors.New("Invalid panel action")
			}
			if err := s.process.SetTerminalConfigSetting(ctx, "theme", row.Get("theme")); err != nil {
				return err
			}
		} else if state.props.Kind != "config" {
			return errors.New("Invalid panel action")
		}
		if err := s.emitTerminalConfig(ctx, emit); err != nil {
			return err
		}
		s.mu.Lock()
		s.terminalPanel = nil
		s.mu.Unlock()
		if emit != nil {
			return emit(tui.Event{Kind: tui.LocalPanelClosed})
		}
		return nil
	case "next-config", "previous-config":
		if state.props.Kind != "config" {
			return errors.New("Invalid panel action")
		}
		err = s.changeTerminalConfig(ctx, state, row, result.Action == "previous-config")
		if err == nil {
			err = s.emitTerminalConfig(ctx, emit)
		}
	case "edit-config":
		if state.props.Kind != "config" || row.String("key") != "language" && row.String("key") != "outputStyle" && row.String("key") != "model" && row.String("key") != "autoUpdatesChannel" && row.String("key") != "theme" && row.String("key") != "showExternalIncludesDialog" && row.String("key") != "teammateDefaultModel" {
			return errors.New("Invalid panel action")
		}
		if row.String("key") == "showExternalIncludesDialog" {
			err = s.openTerminalExternalIncludes(ctx, state)
			form = true
			break
		}
		if row.String("key") == "theme" {
			err = s.openTerminalThemePicker(ctx, state)
			form = true
			break
		}
		if row.String("key") == "autoUpdatesChannel" {
			form, err = s.openTerminalConfigChannel(ctx, state)
			if err == nil && !form {
				err = s.emitTerminalConfig(ctx, emit)
			}
			break
		}
		if row.String("key") == "model" || row.String("key") == "teammateDefaultModel" {
			var picker tui.ModelPicker
			var err error
			if row.String("key") == "teammateDefaultModel" {
				picker, err = s.process.NativeTeammateModelPicker(ctx)
			} else {
				picker, err = s.process.NativeModelPicker(ctx, false)
			}
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.modelPicker = &pendingModelPicker{id: picker.ID, config: state, teammate: row.String("key") == "teammateDefaultModel"}
			s.mu.Unlock()
			if emit != nil {
				return emit(tui.Event{Kind: tui.ModelPickerOpened, ModelPicker: &picker})
			}
			return nil
		}
		if row.String("key") == "outputStyle" {
			err = s.openTerminalOutputStylePicker(ctx, state)
			form = true
			break
		}
		state.form, state.selected = "config-edit", row.String("key")
		state.props.Submenu = true
		state.props.DismissAction = "config-back"
		state.props.Title = "Select " + state.selected
		state.props.Fields = []tui.LocalPanelField{{Name: "value", Label: state.selected, Value: configLabel(row.Get("value"))}}
		state.props.Description = "Enter your preferred response and voice language. Leave empty for default (English)."
		if state.selected == "language" && string(row.Get("value")) == `""` {
			state.props.Fields[0].Value = ""
		}
		form = true
	case "edit-memory":
		if state.props.Kind != "memory" {
			return errors.New("Invalid panel action")
		}
		state.form = "memory-edit"
		state.selected = result.Row
		state.props.Title = "Edit memory"
		state.props.Description = row.String("path")
		state.props.Fields = []tui.LocalPanelField{{Name: "content", Label: "Content", Value: row.String("content")}}
		form = true
	case "refresh":
	case "add":
		if state.props.Kind != "permissions" {
			return errors.New("Invalid panel action")
		}
		state.form = "permission-add"
		state.props.Title = "Add permission rule"
		state.props.Fields = []tui.LocalPanelField{{Name: "behavior", Label: "Behavior (allow/deny/ask)", Value: "allow"}, {Name: "source", Label: "Source (userSettings/projectSettings/localSettings/session)", Value: "userSettings"}, {Name: "rule", Label: "Rule (for example Bash(git status:*) )"}}
		form = true
	case "remove":
		if state.props.Kind != "permissions" {
			return errors.New("Invalid panel action")
		}
		err = s.process.TerminalPermissionRule(ctx, "removeRules", row.String("behavior"), row.String("source"), row.String("rule"))
	case "create", "edit":
		if state.props.Kind != "agents" {
			return errors.New("Invalid panel action")
		}
		state.form = result.Action
		state.selected = result.Row
		state.props.Title = "Create agent"
		scope := "user"
		name := ""
		description := ""
		prompt := ""
		model := "inherit"
		tools := ""
		if result.Action == "edit" {
			state.props.Title = "Edit agent"
			name = row.String("agentType")
			description = row.String("description")
			prompt = row.String("systemPrompt")
			model = row.String("modelDisplay")
			if row.String("source") == "projectSettings" {
				scope = "project"
			}
			var names []string
			_ = json.Unmarshal(row.Get("tools"), &names)
			tools = strings.Join(names, ",")
		}
		state.props.Fields = []tui.LocalPanelField{{Name: "name", Label: "Name", Value: name}, {Name: "scope", Label: "Scope (user/project)", Value: scope}, {Name: "description", Label: "Description", Value: description}, {Name: "systemPrompt", Label: "System prompt", Value: prompt}, {Name: "model", Label: "Model", Value: model}, {Name: "tools", Label: "Tools (comma separated; empty inherits)", Value: tools}}
		form = true
	case "delete":
		if state.props.Kind != "agents" {
			return errors.New("Invalid panel action")
		}
		scope := "user"
		if row.String("source") == "projectSettings" {
			scope = "project"
		}
		_, err = s.process.TerminalAgents(ctx, "DELETE", row.String("agentType"), raw(map[string]any{"scope": scope, "target": row.String("target")}))
	case "save":
		switch state.form {
		case "config-edit":
			value := result.Fields["value"]
			if state.selected == "language" {
				value = terminalConfigLanguage(value)
			}
			err = s.process.SetTerminalConfigSetting(ctx, state.selected, raw(value))
			if err == nil {
				if state.configDirty == nil {
					state.configDirty = map[string]bool{}
				}
				state.configDirty[state.selected] = true
				err = s.emitTerminalConfig(ctx, emit)
			}
		case "memory-edit":
			err = s.process.WriteTerminalMemory(ctx, state.selected, result.Fields["content"])
		case "permission-add":
			err = s.process.TerminalPermissionRule(ctx, "addRules", result.Fields["behavior"], result.Fields["source"], result.Fields["rule"])
		case "create", "edit":
			fields := map[string]any{}
			for _, key := range []string{"name", "scope", "description", "systemPrompt", "model"} {
				fields[key] = result.Fields[key]
			}
			if names := strings.TrimSpace(result.Fields["tools"]); names != "" {
				values := strings.Split(names, ",")
				for i := range values {
					values[i] = strings.TrimSpace(values[i])
				}
				fields["tools"] = values
			}
			method, name := "POST", ""
			if state.form == "edit" {
				old := state.rows[state.selected]
				method, name = "PUT", old.String("agentType")
				delete(fields, "name")
				fields["target"] = old.String("target")
				if old.String("source") == "projectSettings" {
					fields["scope"] = "project"
				} else {
					fields["scope"] = "user"
				}
			}
			_, err = s.process.TerminalAgents(ctx, method, name, raw(fields))
		default:
			return errors.New("No local form is active")
		}
	case "toggle", "reconnect":
		if state.props.Kind == "fast" {
			enabled := string(row.Get("enabled")) != "true"
			_, err = s.process.TerminalFastMode(ctx, &enabled)
		} else if state.props.Kind == "mcp" {
			var enabled *bool
			if result.Action == "toggle" {
				value := row.String("status") == "disabled"
				enabled = &value
			}
			err = s.process.TerminalMCPControl(ctx, row.String("name"), enabled)
		} else {
			return errors.New("Invalid panel action")
		}
	case "stop":
		if state.props.Kind != "tasks" {
			return errors.New("Invalid panel action")
		}
		switch row.String("type") {
		case "in_process_teammate":
			_, err = s.process.KillTeammate(ctx, row.String("id"))
		case "dream":
			err = s.process.StopNativeAutoDream(ctx, row.String("id"))
		default:
			_, err = s.process.StopBackgroundTask(ctx, row.String("id"))
		}
	case "output":
		if state.props.Kind != "tasks" {
			return errors.New("Invalid panel action")
		}
		var text string
		text, err = s.process.TerminalTaskOutput(ctx, row.String("id"))
		if err == nil {
			state.props.Rows = []tui.LocalPanelRow{{ID: result.Row, Label: row.String("description"), Detail: text}}
			state.props.Title = "Task output"
			state.props.Description = text
			form = true
		}
	case "foreground":
		if state.props.Kind != "tasks" || row.String("type") != "in_process_teammate" {
			return errors.New("Invalid panel action")
		}
		err = s.process.SetTeammateView(ctx, row.String("id"))
		if err == nil {
			s.mu.Lock()
			s.terminalPanel = nil
			s.mu.Unlock()
			if emit != nil {
				return emit(tui.Event{Kind: tui.LocalPanelClosed})
			}
			return nil
		}
	default:
		return errors.New("Unknown local panel action")
	}
	if err != nil {
		state.props.Notice = err.Error()
	} else if !form {
		state.props.Notice = ""
		err = s.refreshTerminalPanel(ctx, state)
		if err != nil {
			state.props.Notice = err.Error()
		}
	}
	if emit != nil {
		props := terminalPanelProjection(state.props)
		return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
	}
	return err
}

var _ tui.LocalPanelSession = (*Session)(nil)
