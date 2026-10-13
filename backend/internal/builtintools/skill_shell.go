package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Prompt expansion borrows this tool's original permission consumer with one
// temporary getAppState overlay. The owner, caches, native binding and real
// registered Call are unchanged; no process-wide permission state is mutated.
func (s *Service) PromptShellPermission(scope *cooperative.Scope, in *protocol.JSONObject, permission protocol.JSONObject) (queryloop.ToolPermission, error) {
	return s.promptNamedShellPermission(scope, "Bash", in, permission)
}

func (s *Service) PromptPowerShellPermission(scope *cooperative.Scope, in *protocol.JSONObject, permission protocol.JSONObject) (queryloop.ToolPermission, error) {
	return s.promptNamedShellPermission(scope, "PowerShell", in, permission)
}

func (s *Service) promptNamedShellPermission(scope *cooperative.Scope, name string, in *protocol.JSONObject, permission protocol.JSONObject) (queryloop.ToolPermission, error) {
	if e := s.check(scope); e != nil {
		return queryloop.ToolPermission{}, e
	}
	borrowed := *s
	borrowed.options = s.options
	borrowed.options.PermissionContext = func(current *cooperative.Scope) (protocol.JSONObject, error) {
		if e := s.check(current); e != nil {
			return protocol.JSONObject{}, e
		}
		return permission.Clone(), nil
	}
	return borrowed.PermissionDecision(scope, name, in)
}
