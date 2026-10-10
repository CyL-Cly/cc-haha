package usercontext

// MemoryFilesForInspection reuses the current memory producer and its existing
// native cache. It does not reconstruct or parse the rendered claudeMd prefix.
func (s *Service) MemoryFilesForInspection() ([]MemoryFile, error) {
	mode, err := s.InstructionFilesMode()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	memory := s.options.Memory
	directories := append([]string{}, s.options.AdditionalDirectories...)
	s.mu.Unlock()
	if memory == nil {
		return nil, &CapabilityError{"inspection requires original memory producer"}
	}
	return memory.NormalMemoryFiles(mode, directories, false)
}
