package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

var _ tui.MessageSelectorSession = (*Session)(nil)

func (s *Session) MessageSelectorSnapshot(ctx context.Context) (tui.MessageSelectorSnapshot, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return tui.MessageSelectorSnapshot{}, cooperative.ErrClosed
	}
	return s.process.TerminalMessageSelector(ctx)
}
func (s *Session) SelectMessage(ctx context.Context, selection tui.MessageSelection, emit tui.Emit) (tui.MessageSelectionResult, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return tui.MessageSelectionResult{}, cooperative.ErrClosed
	}
	modelCtx, cancel := context.WithCancel(ctx)
	s.cancel, s.emit = cancel, emit
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancel, s.emit = nil, nil; s.mu.Unlock() }()
	return s.process.TerminalSelectMessage(modelCtx, selection)
}
