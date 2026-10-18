package taskstore

// NativeConfigDirectory exposes the existing native path producer to team
// config and inbox consumers that borrow this same task-store singleton.
// Finite stores retain each consumer's own fixed ConfigDir fallback.
func (s *Service) NativeConfigDirectory() (string, bool) {
	if s == nil || s.options.NativeConfigDirectory == nil {
		return "", false
	}
	return s.options.NativeConfigDirectory(), true
}

func (s *Service) configDirectory() string {
	if directory, bound := s.NativeConfigDirectory(); bound {
		return directory
	}
	return s.options.ConfigDir
}
