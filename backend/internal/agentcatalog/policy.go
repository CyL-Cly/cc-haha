package agentcatalog

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Restricted uses the same managed Source policy as discovery, so API mutation
// cannot derive authorization from a merged or user-overridden settings field.
func (s *Service) Restricted(scope *cooperative.Scope) (bool, error) {
	if err := s.check(scope); err != nil {
		return false, err
	}
	return s.locked()
}
