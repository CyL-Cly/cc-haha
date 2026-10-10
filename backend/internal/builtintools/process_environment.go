package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (s *Service) processEnvironment(scope *cooperative.Scope) (map[string]string, error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	env := s.options.Environment
	if s.options.ProcessEnvironment != nil {
		var err error
		env, err = s.options.ProcessEnvironment(scope)
		if err != nil {
			return nil, err
		}
	}
	copy := make(map[string]string, len(env))
	for key, value := range env {
		copy[key] = value
	}
	return copy, nil
}
