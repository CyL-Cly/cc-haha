package application

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// Status publication only schedules terminal work. Refresh runs through the
// existing native action owner, avoiding reentry while an action retires a row.
func (s *Session) SubscribeTerminalPluginInstallation(ctx context.Context, changed func(json.RawMessage)) (json.RawMessage, func(), error) {
	if s.process == nil {
		return nil, nil, nil
	}
	initial, remove, err := s.process.SubscribeNativePluginInstallation(ctx, func(value protocol.JSONObject) {
		if ctx.Err() == nil && changed != nil {
			changed(raw(value))
		}
	})
	return raw(initial), remove, err
}
func (s *Session) refreshTerminalPluginInstallation(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if result.Action != "plugin-installation-refresh" && result.Action != "mcp-catalog-refresh" {
		return false, nil
	}
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil {
		return true, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	s.mu.Lock()
	active := s.terminalPanel == state
	s.mu.Unlock()
	if !active || state.props.ID != result.ID || state.props.Kind != "plugin" && state.props.Kind != "mcp" {
		return true, nil
	}
	if result.Action == "mcp-catalog-refresh" && !state.props.MCPCatalog {
		return true, nil
	}
	if result.Action == "plugin-installation-refresh" && state.props.Kind != "plugin" {
		return true, nil
	}
	if len(state.props.Fields) > 0 {
		return true, nil
	}
	if err := s.refreshTerminalPanel(ctx, state); err != nil {
		return true, err
	}
	// Refreshed is fenced again on the terminal loop. An already queued result
	// cannot recreate this panel after the user closed or switched it.
	props := terminalPanelProjection(state.props)
	if emit != nil {
		return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props, Background: true})
	}
	return true, nil
}
