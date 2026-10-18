package usercontext

// MemoryFilesForInspection reuses the current memory producer and its existing
// native cache. It does not reconstruct or parse the rendered claudeMd prefix.
func (s *Service) MemoryFilesForInspection() ([]MemoryFile, error) {
	return s.memoryFilesForInspection(false)
}

// Config's external-import gate uses the original forced-discovery memo.
func (s *Service) ExternalMemoryFilesForInspection() ([]MemoryFile, error) {
	return s.memoryFilesForInspection(true)
}

func (s *Service) memoryFilesForInspection(forceExternal bool) ([]MemoryFile, error) {
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
	return memory.NormalMemoryFiles(mode, directories, forceExternal)
}

// Approval changes apply to the next native discovery. Existing memory/context
// memos retain their Source lifecycle until an explicit clear or refresh.
func (s *Service) SetExternalIncludesApproved(approved bool) {
	s.mu.Lock()
	memory := s.options.Memory
	s.mu.Unlock()
	if memory != nil && memory.Normal != nil {
		memory.Normal.mu.Lock()
		memory.Normal.IncludeExternal = approved
		memory.Normal.mu.Unlock()
	}
}
