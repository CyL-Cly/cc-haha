package usercontext

// ClearWorktreeMemoryFiles clears only the native file discovery caches. It
// does not clear the existing session/date/user-context promise.
func (s *Service) ClearWorktreeMemoryFiles() {
	s.mu.Lock()
	memory := s.options.Memory
	s.mu.Unlock()
	if memory != nil {
		memory.ClearMemoryFileCaches()
	}
}
