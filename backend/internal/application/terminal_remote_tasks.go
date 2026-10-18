package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Source REPL's mount effect is conditional on its genuine initial history,
// rather than the live messages that may already change after the first paint.
func (s *Session) StartTerminalRemoteTasks(ctx context.Context) error {
	if s == nil || s.process == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	s.mu.Lock()
	closed := s.closed
	history := len(s.initialTUIRecords)
	s.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	if history == 0 {
		return nil
	}
	return s.process.StartTerminalRemoteTasks(ctx)
}
