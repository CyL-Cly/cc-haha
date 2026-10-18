package application

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func initializeTerminalSettingsTabs(state *terminalLocalPanelState, headerFocused bool) {
	if state.props.Kind == "config" || state.props.Kind == "status" || state.props.Kind == "usage" {
		state.props.SettingsTabs = []string{"status", "config", "usage"}
		state.props.SettingsHeaderFocused = headerFocused
	}
}

func (s *Session) applyTerminalSettingsTab(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if !strings.HasPrefix(result.Action, "settings-tab:") && result.Action != "settings-dismiss" {
		return false, nil
	}
	kind := strings.TrimPrefix(result.Action, "settings-tab:")
	if result.Action != "settings-dismiss" && kind != "status" && kind != "config" && kind != "usage" {
		return true, errors.New("Unknown settings tab")
	}
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	if previous == nil || previous.props.ID != result.ID {
		return true, errors.New("Settings dialog is no longer active")
	}
	previous.mu.Lock()
	valid := len(previous.props.SettingsTabs) > 0 && !previous.props.Submenu
	previous.mu.Unlock()
	if !valid {
		return true, errors.New("Settings tab navigation is unavailable")
	}
	// Source Tab unmounts Config. Its immediate writes are retained; entering
	// Config again captures a new rollback snapshot. Each mount also receives
	// a new ID so a queued asynchronous result cannot overwrite a later mount.
	s.closeTerminalUsagePanel(previous)
	if result.Action == "settings-dismiss" {
		s.mu.Lock()
		if s.terminalPanel == previous {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelClosed})
		}
		return true, nil
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: kind}, rows: map[string]protocol.JSONObject{}}
	initializeTerminalSettingsTabs(state, true)
	if err := s.refreshTerminalPanel(ctx, state); err != nil {
		return true, err
	}
	s.mu.Lock()
	if s.terminalPanel != previous {
		s.mu.Unlock()
		return true, errors.New("Settings dialog changed before tab navigation")
	}
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
	return true, nil
}

func terminalPanelProjection(value tui.LocalPanel) tui.LocalPanel {
	value.Rows = append([]tui.LocalPanelRow(nil), value.Rows...)
	for index := range value.Rows {
		value.Rows[index].Actions = append([]tui.LocalPanelAction(nil), value.Rows[index].Actions...)
	}
	value.Fields = append([]tui.LocalPanelField(nil), value.Fields...)
	value.Actions = append([]tui.LocalPanelAction(nil), value.Actions...)
	value.RawInputKeys = append([]string(nil), value.RawInputKeys...)
	value.SettingsTabs = append([]string(nil), value.SettingsTabs...)
	value.Presentation = append([]byte(nil), value.Presentation...)
	return value
}
