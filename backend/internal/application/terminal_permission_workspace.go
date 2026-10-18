package application

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalWorkspacePanelState struct{ level, path, input string }

func (s *Session) refreshTerminalPermissionWorkspace(ctx context.Context, state *terminalLocalPanelState) error {
	workspace := state.workspacePanel
	p := &state.props
	p.HideDetails = true
	p.Actions = []tui.LocalPanelAction{{Key: "left", Name: "workspace-back", Label: "back"}}
	p.DismissAction = "workspace-back"
	p.Title = "Workspace"
	if workspace.level == "add" {
		state.form = "workspace-add"
		p.Title = "Add working directory"
		p.Fields = []tui.LocalPanelField{{Name: "path", Label: "Directory path", Value: workspace.input}}
		return nil
	}
	if workspace.level == "remove" {
		p.Title = "Remove directory from workspace?"
		p.Description = workspace.path + "\nClaude Code will no longer have access to files in this directory."
		for _, choice := range []struct{ id, label string }{{"yes", "Yes"}, {"no", "No"}} {
			state.rows[choice.id] = protocol.NewJSONObject()
			p.Rows = append(p.Rows, tui.LocalPanelRow{ID: choice.id, Label: choice.label, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "workspace-remove-" + choice.id, Label: "select"}}})
		}
		return nil
	}
	current, err := s.process.TerminalPanelSnapshot(ctx, "permissions")
	if err != nil {
		return err
	}
	state.form = ""
	p.Description = "-  " + current.String("originalWorkingDirectory") + " (Original working directory)"
	for _, directory := range sdkpermissions.WorkspaceDirectoryEntries(current) {
		path := directory.String("path")
		state.rows[path] = directory
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: path, Label: path, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "workspace-remove", Label: "remove directory"}}})
	}
	state.rows["add-directory"] = protocol.NewJSONObject()
	p.Rows = append(p.Rows, tui.LocalPanelRow{ID: "add-directory", Label: "Add directory…", Actions: []tui.LocalPanelAction{{Key: "enter", Name: "workspace-add", Label: "add directory"}}})
	return nil
}
func (s *Session) applyTerminalPermissionWorkspace(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult) error {
	if result.Action == "workspace-open" {
		state.workspacePanel = &terminalWorkspacePanelState{}
		return nil
	}
	workspace := state.workspacePanel
	if workspace == nil {
		return errors.New("Workspace dialog is no longer active")
	}
	switch result.Action {
	case "workspace-back":
		if workspace.level != "" {
			workspace.level = ""
			state.form = ""
		} else {
			state.workspacePanel = nil
		}
	case "workspace-add":
		workspace.level = "add"
		workspace.input = ""
	case "workspace-remove":
		if row, ok := state.rows[result.Row]; !ok || row.String("path") == "" {
			return errors.New("Directory row is no longer available")
		}
		workspace.level, workspace.path = "remove", result.Row
	case "workspace-remove-no":
		workspace.level = ""
	case "workspace-remove-yes":
		if workspace.level != "remove" {
			return errors.New("Directory confirmation is no longer active")
		}
		if err := s.process.TerminalWorkspaceDirectory(ctx, workspace.path, true, false); err != nil {
			return err
		}
		state.props.Notice = "Removed directory " + workspace.path + " from workspace"
		workspace.level = ""
	case "save":
		if workspace.level != "add" {
			return errors.New("Directory input is no longer active")
		}
		workspace.input = result.Fields["path"]
		validation, err := s.process.ValidateTerminalDirectory(ctx, workspace.input)
		if err != nil {
			return err
		}
		if validation.String("resultType") != "success" {
			state.props.Notice = terminalDirectoryHelp(validation)
			return nil
		}
		if err = s.process.TerminalWorkspaceDirectory(ctx, validation.String("absolutePath"), false, false); err != nil {
			return err
		}
		state.props.Notice = "Added directory " + validation.String("absolutePath") + " to workspace for this session"
		workspace.level = ""
		state.form = ""
	default:
		return errors.New("Unknown workspace dialog action")
	}
	return nil
}
