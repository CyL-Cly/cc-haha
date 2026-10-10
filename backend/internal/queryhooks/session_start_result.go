package queryhooks

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Startup results are consumed at loadInitialMessages' actual await boundary.
func (s *Service) TakeSessionStartResult(scope *cooperative.Scope, sid string) (SessionStartResult, error) {
	if s.options.Commands == nil {
		return SessionStartResult{}, nil
	}
	if scope == nil || !scope.BelongsTo(s.options.Commands.Owner) {
		return SessionStartResult{}, cooperative.ErrRestricted
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.startupResults[sid]
	delete(s.startupResults, sid)
	return result, nil
}

func (s *Service) TakeInitialUserMessage(scope *cooperative.Scope) (json.RawMessage, error) {
	if s.options.Commands == nil {
		return nil, nil
	}
	if scope == nil || !scope.BelongsTo(s.options.Commands.Owner) {
		return nil, cooperative.ErrRestricted
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append(json.RawMessage(nil), s.initialUserMessage...)
	s.initialUserMessage = nil
	return result, nil
}
