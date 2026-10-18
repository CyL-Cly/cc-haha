package application

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalBuddyCommands() []tui.Completion {
	return []tui.Completion{{Name: "buddy", Description: "Meet your companion", ArgumentHint: "[hatch|pet|mute|unmute|info]", Kind: "local-jsx"}}
}

func (s *Session) TerminalBuddySnapshot(ctx context.Context) (json.RawMessage, error) {
	return s.process.TerminalBuddySnapshot(ctx)
}

func (s *Session) invokeTerminalBuddyCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "buddy" || !s.terminalInteractive {
		return false, nil
	}
	value, err := s.process.ApplyTerminalBuddy(ctx, args)
	if err != nil {
		return true, err
	}
	if emit != nil {
		if err = emit(tui.Event{Kind: tui.TerminalBuddyChanged, Result: raw(value), Background: true}); err != nil {
			return true, err
		}
	}
	if string(value.Get("done")) == "true" {
		if err = s.completeTerminalLocalJSX(ctx, "buddy", args, value.Get("output"), "system", emit); err != nil {
			return true, err
		}
		return true, nil
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "buddy", HideDetails: true, Presentation: raw(value), RawInputKeys: []string{"q"}, Actions: []tui.LocalPanelAction{{Key: "q", Name: "buddy-close"}}}, rows: map[string]protocol.JSONObject{}, form: args}
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	return true, s.openTerminalConversationPanel(state, emit)
}

func (s *Session) applyTerminalBuddyPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state.props.Kind != "buddy" {
		return false, nil
	}
	if result.Action != "buddy-close" && result.Action != "close" {
		return true, nil
	}
	if err := s.completeTerminalLocalJSX(ctx, "buddy", state.form, nil, "", emit); err != nil {
		return true, err
	}
	return true, s.closeTerminalConversationPanel(state.props.ID, emit)
}
