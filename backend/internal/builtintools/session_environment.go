package builtintools

import (
	"context"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// The raw model command remains the permission/AST/task identity. This is the
// original session-environment stage of the eventual shell execution string.
func (s *Service) sessionEnvironmentCommand(ctx context.Context, scope *cooperative.Scope, command string) (string, error) {
	if s.options.SessionEnvironmentScript == nil {
		return command, nil
	}
	script, err := s.options.SessionEnvironmentScript(ctx, scope)
	if err != nil {
		return "", err
	}
	if script == nil || *script == "" {
		return command, nil
	}
	quoted := "'" + strings.ReplaceAll(command, "'", "'\\''") + "'"
	return *script + " && eval " + quoted, nil
}
