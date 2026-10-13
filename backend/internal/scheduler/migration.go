package scheduler

// Count the owned executions rather than persisted, potentially stale run rows.
func (s *Service) MigrationRunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}
