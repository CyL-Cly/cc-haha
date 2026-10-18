package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) refreshActiveTerminalPanel(ctx context.Context, emit tui.Emit) error {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil {
		return nil
	}
	return s.refreshTerminalPanelIfActive(ctx, state, emit)
}

func (s *Session) refreshTerminalPanelIfActive(ctx context.Context, state *terminalLocalPanelState, emit tui.Emit) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	// A timer can capture this panel immediately before Save/Pause closes it.
	// Recheck after its serialized action finishes so an old poll cannot reopen
	// a dialog whose active-session ownership has already ended.
	s.mu.Lock()
	active := s.terminalPanel == state
	s.mu.Unlock()
	if !active {
		return nil
	}
	if state.props.Kind != "tasks" && state.props.Kind != "workflows" || state.form != "" || state.props.Title == "Task output" {
		return nil
	}
	if err := s.refreshTerminalPanel(ctx, state); err != nil {
		return err
	}
	if emit != nil {
		return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &state.props})
	}
	return nil
}
