package usercontext

// ResetAfterCompact preserves child/shared-memory isolation. The main thread
// clears both outer context memo and memory discovery so InstructionsLoaded
// can run with source=compact on the next context build.
func (s *Service) ResetAfterCompact(main bool) {
	if s == nil || !main {
		return
	}
	s.ClearUserContext()
	if s.options.Memory != nil {
		s.options.Memory.ResetMemoryFiles("compact")
	}
}
