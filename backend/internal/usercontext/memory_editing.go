package usercontext

// RefreshMemoryFiles is the local /memory editor's explicit cache boundary.
// The same native discovery memo is cleared before reloading model context.
func (s *Service) RefreshMemoryFiles() {
	s.mu.Lock()
	memory := s.options.Memory
	s.future = nil
	s.mu.Unlock()
	if memory != nil {
		memory.ClearMemoryFileCaches()
	}
}
