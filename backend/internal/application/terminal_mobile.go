package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalMobileCommands() []tui.Completion {
	result := []tui.Completion{}
	for _, name := range []string{"mobile", "ios", "android"} {
		result = append(result, tui.Completion{Name: name, Description: "Show QR code to download the Claude mobile app", Kind: "local-jsx"})
	}
	return result
}

func (s *Session) invokeTerminalMobileCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "mobile" && name != "ios" && name != "android" || !s.terminalInteractive {
		return false, nil
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "mobile", HideDetails: true, RawInputKeys: []string{"tab", "left", "right", "q"}, Actions: []tui.LocalPanelAction{{Key: "tab", Name: "mobile-switch"}, {Key: "left", Name: "mobile-switch"}, {Key: "right", Name: "mobile-switch"}, {Key: "q", Name: "mobile-close"}}, Presentation: tui.TerminalMobilePresentation("ios")}, rows: map[string]protocol.JSONObject{}, selected: "ios", form: args}
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	return true, s.openTerminalConversationPanel(state, emit)
}

func (s *Session) applyTerminalMobilePanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state.props.Kind != "mobile" {
		return false, nil
	}
	if result.Action == "mobile-close" || result.Action == "close" {
		if err := s.completeTerminalLocalJSX(ctx, "mobile", state.form, nil, "", emit); err != nil {
			return true, err
		}
		return true, s.closeTerminalConversationPanel(state.props.ID, emit)
	}
	if result.Action != "mobile-switch" {
		return true, nil
	}
	state.selected = "android"
	if value := obj(state.props.Presentation); value.String("platform") == "android" {
		state.selected = "ios"
	}
	state.props.Presentation = tui.TerminalMobilePresentation(state.selected)
	if emit != nil {
		value := terminalPanelProjection(state.props)
		return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &value})
	}
	return true, nil
}
