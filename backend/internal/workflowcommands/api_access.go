package workflowcommands

import (
	"errors"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// MetadataOnOwner shares the existing parser with the Workflow API/runtime.
// Every parser operation still verifies this same owner scope.
func (s *Service) MetadataOnOwner(scope *cooperative.Scope) (*Metadata, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	return s.metadata, nil
}

// ProjectWorkflowsDirectory is Source resolveProjectWorkflowsDir using the
// same discovery walk/root resolver. Unported security profiles stay explicit.
func (s *Service) ProjectWorkflowsDirectory(scope *cooperative.Scope, cwd string) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	dirs, e := s.projectDirectories(scope, cwd)
	if e != nil {
		var cap *CapabilityError
		if errors.As(e, &cap) {
			return "", e
		}
		dirs = nil
	}
	if len(dirs) > 0 {
		return dirs[0], nil
	}
	root, e := s.options.Git.FindRoot(scope, cwd)
	if e != nil {
		return "", e
	}
	if root == "" {
		root = cwd
	}
	return filepath.Join(root, ".claude", "workflows"), nil
}
