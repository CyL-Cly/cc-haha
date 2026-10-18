package usercontext

// ClearTerminalSession preserves the settings memo. Source /clear resets the
// session date, injection and memory discovery; /config itself does not.
func (s *Service) ClearTerminalSession() {
	s.mu.Lock()
	s.future = nil
	s.sessionSet = false
	s.session = nil
	s.injection = nil
	s.cachedMemory = nil
	memory := s.options.Memory
	s.mu.Unlock()
	if memory != nil {
		memory.ResetMemoryFiles("session_start")
	}
}
