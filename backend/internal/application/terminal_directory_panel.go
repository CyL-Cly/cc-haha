package application

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"path/filepath"
)

type terminalDirectoryPanelState struct{ path, input string }

func terminalDirectoryHelp(result protocol.JSONObject) string {
	switch result.String("resultType") {
	case "emptyPath":
		return "Please provide a directory path."
	case "pathNotFound":
		return "Path " + result.String("absolutePath") + " was not found."
	case "notADirectory":
		return result.String("directoryPath") + " is not a directory. Did you mean to add the parent directory " + filepath.Dir(result.String("absolutePath")) + "?"
	case "alreadyInWorkingDirectory":
		return result.String("directoryPath") + " is already accessible within the existing working directory " + result.String("workingDir") + "."
	default:
		return "Added " + result.String("absolutePath") + " as a working directory."
	}
}
func (s *Session) refreshTerminalDirectoryPanel(state *terminalLocalPanelState) error {
	panel := state.directoryPanel
	if panel == nil {
		return errors.New("Directory dialog is no longer active")
	}
	p := &state.props
	p.Title = "Add working directory"
	p.HideDetails = true
	p.Actions = nil
	if panel.path == "" {
		state.form = "directory-input"
		p.Description = "Add a directory Claude can access for this session."
		p.Fields = []tui.LocalPanelField{{Name: "path", Label: "Directory path", Value: panel.input}}
		return nil
	}
	p.Description = panel.path
	for _, option := range []struct{ id, label string }{{"session", "Yes, for this session"}, {"remember", "Yes, and remember in local settings"}, {"cancel", "No"}} {
		state.rows[option.id] = protocol.NewJSONObject()
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: option.id, Label: option.label, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "directory-" + option.id, Label: "select"}}})
	}
	return nil
}
func (s *Session) applyTerminalDirectoryPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	panel := state.directoryPanel
	if panel == nil {
		return false, errors.New("Directory dialog is no longer active")
	}
	if result.Action == "directory-cancel" {
		if emit != nil {
			message := "Did not add a working directory."
			if panel.path != "" {
				message = "Did not add " + panel.path + " as a working directory."
			}
			if err := emit(tui.Event{Kind: tui.Notice, Text: message}); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	remember := result.Action == "directory-remember"
	path := panel.path
	if result.Action == "save" {
		if state.form != "directory-input" {
			return false, errors.New("Directory input is no longer active")
		}
		panel.input = result.Fields["path"]
		validation, err := s.process.ValidateTerminalDirectory(ctx, panel.input)
		if err != nil {
			return false, err
		}
		if validation.String("resultType") != "success" {
			state.props.Notice = terminalDirectoryHelp(validation)
			return false, nil
		}
		path = validation.String("absolutePath")
	} else if result.Action != "directory-session" && !remember {
		return false, errors.New("Unknown directory dialog action")
	}
	applied, err := s.process.AddTerminalDirectory(ctx, path, remember)
	if err != nil {
		return false, err
	}
	if applied.String("resultType") != "success" {
		state.props.Notice = terminalDirectoryHelp(applied)
		return false, nil
	}
	message := "Added " + applied.String("absolutePath") + " as a working directory for this session"
	if remember {
		message = "Added " + applied.String("absolutePath") + " as a working directory and saved to local settings"
	}
	if failure := applied.String("persistenceError"); failure != "" {
		message = "Added " + applied.String("absolutePath") + " as a working directory. Failed to save to local settings: " + failure
	}
	if emit != nil {
		if err = emit(tui.Event{Kind: tui.Notice, Text: message + " · /permissions to manage"}); err != nil {
			return false, err
		}
	}
	return true, nil
}
