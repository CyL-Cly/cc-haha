package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) invokeTerminalClearCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	switch name {
	case "clear", "reset", "new":
	default:
		return false, nil
	}
	if !s.terminalInteractive {
		return false, nil
	}
	result, err := s.process.TerminalClear(ctx, args)
	if err != nil {
		return true, err
	}
	// The submitted local-command caller explicitly clears local JSX. Match
	// that unmount in both the UI projection and the owned application worker.
	s.mu.Lock()
	previous := s.terminalPanel
	s.terminalPanel = nil
	s.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	s.closeTerminalBtwPanel(previous)
	s.closeTerminalTeamsPanel(previous)
	if emit != nil {
		return true, emit(tui.Event{Kind: tui.ConversationCleared, Result: raw(result)})
	}
	return true, nil
}
