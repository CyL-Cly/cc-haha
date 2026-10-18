package toolsearch

// ClearDescriptionCache follows the Source /clear lifecycle without changing
// the registered tool catalogue or its actual token-count transport.
func (s *Service) ClearDescriptionCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]description{}
}
