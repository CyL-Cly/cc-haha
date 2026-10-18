package builtintools

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// WithDeniedToolFork clones the read/cache state of a real auxiliary fork.
// The caller retains the catalog and supplies its deny-all permission callback.
func (s *Service) WithDeniedToolFork(scope *cooperative.Scope) (*Service, func(*cooperative.Scope) error, error) {
	if !scope.IsNativeSideQuestion(s.options.Owner) {
		return nil, nil, cooperative.ErrRestricted
	}
	cache, err := s.options.Cache.CloneOnOwner(scope)
	if err != nil {
		return nil, nil, err
	}
	writer, err := s.options.Write.WithForkCache(scope, cache)
	if err != nil {
		return nil, nil, err
	}
	clone := *s
	clone.options.Cache, clone.options.Write = cache, writer
	clone.agentSnapshot = append(clone.agentSnapshot[:0:0], s.agentSnapshot...)
	return &clone, cache.Clear, nil
}
