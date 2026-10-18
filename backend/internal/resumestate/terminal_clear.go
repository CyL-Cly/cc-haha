package resumestate

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// ClearInvokedSkills retains the same original skill map for preserved agents.
// Clearing the main conversation must not erase a running background agent's
// full skill content, or leave a previous main-thread skill in the next turn.
func (s *Service) ClearInvokedSkills(preserved map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.state.Skills[:0]
	for _, entry := range s.state.Skills {
		value, _ := entry.Object("value")
		if preserved[value.String("agentId")] {
			kept = append(kept, entry)
		}
	}
	for i := len(kept); i < len(s.state.Skills); i++ {
		s.state.Skills[i] = protocol.JSONObject{}
	}
	s.state.Skills = kept
}
