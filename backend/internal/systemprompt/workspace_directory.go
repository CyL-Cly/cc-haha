package systemprompt

// The REPL's query-context producer supplies the current permission map for
// each query. Updating this input gives the section cache the same boundary.
func (s *Service) SetAdditionalWorkingDirectories(directories []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.AdditionalDirectories = append([]string{}, directories...)
	s.cache = make(map[string]*string)
}
