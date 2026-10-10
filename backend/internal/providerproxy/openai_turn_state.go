package providerproxy

import (
	"context"
	"sync"
)

// OpenAICodexTurnState belongs to one user turn. A cancelled context makes the
// captured routing value inaccessible without spawning a lifetime goroutine.
type OpenAICodexTurnState struct {
	mu     sync.Mutex
	ctx    context.Context
	value  string
	closed bool
}

func NewOpenAICodexTurnState(ctx context.Context) *OpenAICodexTurnState {
	return &OpenAICodexTurnState{ctx: ctx}
}
func (s *OpenAICodexTurnState) Get() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return ""
	}
	return s.value
}
func (s *OpenAICodexTurnState) Capture(value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && s.ctx.Err() == nil && s.value == "" && value != "" {
		s.value = value
	}
}
func (s *OpenAICodexTurnState) Dispose() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed, s.value = true, ""
}
