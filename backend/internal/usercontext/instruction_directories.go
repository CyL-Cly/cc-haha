package usercontext

// Source bootstrap updates discovery inputs after /add-dir, while an already
// computed user context remains cached until its normal clear/mode boundary.
func (s *Service) SetAdditionalInstructionDirectories(directories []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.AdditionalDirectories = append([]string{}, directories...)
}
