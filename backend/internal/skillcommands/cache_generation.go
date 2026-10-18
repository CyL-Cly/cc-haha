package skillcommands

// CacheGeneration identifies the actual clearSkillCaches lifetime. Reading it
// neither clears a cache nor substitutes a catalog for an in-flight loader.
func (s *Service) CacheGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}
