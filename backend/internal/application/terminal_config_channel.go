package application

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) openTerminalConfigChannel(ctx context.Context, state *terminalLocalPanelState) (bool, error) {
	snapshot, err := s.process.TerminalConfigState(ctx)
	if err != nil {
		return false, err
	}
	reason := obj(snapshot.Get("autoUpdaterDisabledReason"))
	if !reason.IsObject() && obj(snapshot.Get("initial")).String("autoUpdatesChannel") == "stable" {
		if err = s.process.SetTerminalUpdateChannel(ctx, "latest"); err != nil {
			return false, err
		}
		markConfigDirty(state, "autoUpdatesChannel")
		return false, nil
	}
	state.form = "config-channel"
	state.props.Submenu = true
	state.props.DismissAction = "config-back"
	state.props.Rows = nil
	state.props.Fields = nil
	state.props.Actions = nil
	state.rows = map[string]protocol.JSONObject{}
	state.props.Title = "Switch to Stable Channel"
	state.props.Description = "The stable channel may have an older version than what you're currently running (" + snapshot.String("version") + "). How would you like to handle this?"
	choices := []struct{ id, label string }{{"downgrade", "Allow possible downgrade to stable version"}, {"stay", "Stay on current version (" + snapshot.String("version") + ") until stable catches up"}}
	if reason.IsObject() {
		state.props.Title = "Enable Auto-Updates"
		switch reason.String("type") {
		case "config":
			state.props.Description = "Select an update channel"
			choices = []struct{ id, label string }{{"enable-latest", "Enable with latest channel"}, {"enable-stable", "Enable with stable channel"}}
		case "development":
			state.props.Description = "Auto-updates are disabled in development builds."
			choices = nil
		default:
			state.props.Description = "Auto-updates are controlled by an environment variable and cannot be changed here.\nUnset " + reason.String("envVar") + " to re-enable auto-updates."
			choices = nil
		}
	}
	for _, choice := range choices {
		state.rows[choice.id] = protocol.NewJSONObject(protocol.JSONField{Name: "choice", Value: raw(choice.id)})
		state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: choice.id, Label: choice.label, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "config-channel-select", Label: "select"}}})
	}
	return true, nil
}
func markConfigDirty(state *terminalLocalPanelState, key string) {
	if state.configDirty == nil {
		state.configDirty = map[string]bool{}
	}
	state.configDirty[key] = true
}
func (s *Session) applyTerminalConfigSubmenu(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult) error {
	if state.props.Kind != "config" {
		return errors.New("Invalid Config submenu")
	}
	if result.Action == "config-back" {
		state.form = ""
		state.props.DismissAction = ""
		return nil
	}
	if result.Action != "config-channel-select" || state.form != "config-channel" {
		return errors.New("Invalid channel choice")
	}
	choice := result.Row
	if _, ok := state.rows[choice]; !ok {
		return errors.New("Channel choice is no longer available")
	}
	if err := s.process.SetTerminalUpdateChannel(ctx, choice); err != nil {
		return err
	}
	markConfigDirty(state, "autoUpdatesChannel")
	if choice == "enable-latest" || choice == "enable-stable" {
		markConfigDirty(state, "autoUpdates")
	}
	state.form = ""
	state.props.DismissAction = ""
	return nil
}
