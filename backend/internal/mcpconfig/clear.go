package mcpconfig

// ClearCaches resets the managed-file existence memo. Configuration objects
// and plugin activation still come from their actual shared producers; live
// transports are retained until an explicit in-session component reload.
func (s *Service) ClearCaches() {
	s.mu.Lock()
	s.enterpriseExists = nil
	s.mu.Unlock()
}
