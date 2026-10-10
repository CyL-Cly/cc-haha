package queryhooks

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

const AgentStructuredOutputFeedback = "You MUST call the StructuredOutput tool to complete this request. Call this tool now."

// Source registers this function on Stop (not SubagentStop). Hook agents use
// SubagentStop lookup, so registration must not invent a forced extra turn.
func (s *Service) RegisterAgentStructuredOutput(scope *cooperative.Scope, id string) error {
	if s == nil || s.options.Commands == nil || id == "" || !s.HasNativeAgentBindings(s.options.Commands.Owner) || !scope.BelongsTo(s.options.Commands.Owner) {
		return cooperative.ErrRestricted
	}
	store := s.NativeCommandStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.structuredOutput == nil {
		store.structuredOutput = map[string]string{}
	}
	store.structuredOutput[id] = "Stop"
	return nil
}
func (s *Service) HasAgentStructuredOutput(id, event string) bool {
	store := s.NativeCommandStore()
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.structuredOutput[id] == event
}
