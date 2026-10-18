package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) openTerminalOutputStylePicker(ctx context.Context, state *terminalLocalPanelState) error {
	snapshot, err := s.process.TerminalConfigState(ctx)
	if err != nil {
		return err
	}
	state.form, state.selected = "config-output-style", "outputStyle"
	state.props.Submenu, state.props.HideDetails = true, false
	state.props.DismissAction = "config-back"
	state.props.Title = "Preferred output style"
	state.props.Description = "This changes how Claude Code communicates with you"
	state.props.Fields, state.props.Rows, state.props.Actions = nil, nil, nil
	state.props.RowDescriptions, state.props.VisibleRows = true, 10
	state.props.SelectedRow = obj(snapshot.Get("initial")).String("outputStyle")
	if state.props.SelectedRow == "" {
		state.props.SelectedRow = "default"
	}
	state.rows = map[string]protocol.JSONObject{}
	var styles []protocol.JSONObject
	_ = json.Unmarshal(snapshot.Get("styles"), &styles)
	for _, style := range styles {
		name, label, description := style.String("name"), style.String("name"), style.String("description")
		if name == "default" {
			label = "Default"
			description = "Claude completes coding tasks efficiently and provides concise responses"
		}
		state.rows[name] = style.Clone()
		state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: name, Label: label, Detail: description, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "select-output-style", Label: "select"}}})
	}
	return nil
}

func terminalConfigLanguage(input string) string { return protocol.TrimJavaScriptString(input) }
