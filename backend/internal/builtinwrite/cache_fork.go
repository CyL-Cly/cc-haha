package builtinwrite

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// CloneOnOwner preserves the Source fork's LRU bounds and complete read states,
// without allowing its future reads and writes to mutate the parent's cache.
func (c *FileStateCache) CloneOnOwner(scope *cooperative.Scope) (*FileStateCache, error) {
	rows, err := c.Export(scope)
	if err != nil {
		return nil, err
	}
	clone, err := NewFileStateCache(c.owner, c.max, c.maxBytes)
	if err != nil {
		return nil, err
	}
	if err = clone.Import(scope, rows); err != nil {
		return nil, err
	}
	return clone, nil
}

func (s *Service) WithForkCache(scope *cooperative.Scope, cache *FileStateCache) (*Service, error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if !cache.Owns(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	clone := *s
	clone.options.Cache = cache
	clone.dynamicSkillDirs = map[string]bool{}
	for path, value := range s.dynamicSkillDirs {
		clone.dynamicSkillDirs[path] = value
	}
	clone.unportedSkillDirs = map[string]bool{}
	for path, value := range s.unportedSkillDirs {
		clone.unportedSkillDirs[path] = value
	}
	return &clone, nil
}
