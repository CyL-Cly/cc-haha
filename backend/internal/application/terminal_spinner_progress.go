package application

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) nativeTerminalSpinner(progress tui.TerminalSpinnerProgress) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	copy := progress
	copy.QueryEvent = append(json.RawMessage(nil), progress.QueryEvent...)
	if progress.Verb != nil {
		verb := *progress.Verb
		copy.Verb = &verb
	}
	return s.emitEvent(tui.Event{Kind: tui.TerminalSpinnerProgressChanged, Spinner: &copy})
}
