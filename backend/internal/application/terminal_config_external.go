package application

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) openTerminalExternalIncludes(ctx context.Context, state *terminalLocalPanelState) error {
	snapshot, err := s.process.TerminalConfigState(ctx)
	if err != nil {
		return err
	}
	files := array(snapshot.Get("externalIncludes"))
	if len(files) == 0 {
		return errors.New("No external CLAUDE.md imports remain")
	}
	state.form = "config-external-includes"
	state.props.Submenu, state.props.HideDetails = true, false
	state.props.DismissAction = "config-external-disable"
	state.props.Title = "Allow external CLAUDE.md file imports?"
	paths := []string{}
	for _, file := range files {
		paths = append(paths, "  "+file.String("path"))
	}
	state.props.Description = "This project's CLAUDE.md imports files outside the current working directory. Never allow this for third-party repositories.\nExternal imports:\n" + strings.Join(paths, "\n") + "\nImportant: Only use Claude Code with files you trust. Accessing untrusted files may pose security risks https://code.claude.com/docs/en/security"
	state.props.Fields, state.props.Actions = nil, nil
	state.props.Rows = []tui.LocalPanelRow{{ID: "yes", Label: "Yes, allow external imports", Actions: []tui.LocalPanelAction{{Key: "enter", Name: "config-external-select", Label: "confirm"}}}, {ID: "no", Label: "No, disable external imports", Actions: []tui.LocalPanelAction{{Key: "enter", Name: "config-external-select", Label: "confirm"}}}}
	state.props.SelectedRow = "yes"
	return nil
}

func (s *Session) applyTerminalExternalIncludes(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult) error {
	if state.form != "config-external-includes" {
		return errors.New("External import dialog is no longer active")
	}
	if result.Action != "config-external-disable" && result.Row != "yes" && result.Row != "no" {
		return errors.New("Invalid external import choice")
	}
	if err := s.process.SetTerminalExternalIncludes(ctx, result.Action != "config-external-disable" && result.Row == "yes"); err != nil {
		return err
	}
	state.configExternalTouched = true
	state.form = ""
	return nil
}
