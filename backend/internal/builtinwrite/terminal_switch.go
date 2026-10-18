package builtinwrite

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// ResetTerminalLineChanges is resetCostState's code-change counter reset. It
// preserves the tool's actual read cache, permissions, files and working dir.
func (s *Service) ResetTerminalLineChanges(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	s.addedLines, s.removedLines = 0, 0
	return nil
}
