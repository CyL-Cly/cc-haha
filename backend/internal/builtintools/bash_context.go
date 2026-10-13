package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Bash reads isMainThread/agentId from the actual ToolUseContext. A hook query
// borrows the main handlers, so its temporary context cannot change parent cwd
// or deliver background completion to the parent queue.
func (s *Service) bashContextView(scope *cooperative.Scope, opaque any) (*Service, error) {
	call, ok := opaque.(*builtinwrite.CallContext)
	if !ok || call == nil || call.AgentID == "" {
		return s, nil
	}
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !call.Scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	borrowed := *s
	borrowed.options = s.options
	borrowed.options.NonInteractive = true
	if b := borrowed.options.ShellHints; b != nil {
		copy := *b
		copy.AgentID = call.AgentID
		borrowed.options.ShellHints = &copy
	}
	if cwd := s.options.BashCwd; cwd != nil {
		copy := *cwd
		copy.PreventChanges = true
		borrowed.options.BashCwd = &copy
	}
	if b := s.options.BackgroundTasks; b != nil {
		copy := *b
		copy.AgentID = call.AgentID
		if b.NotifyForAgent != nil {
			copy.Notify = func(current *cooperative.Scope, _ string, message string) error {
				return b.NotifyForAgent(current, call.AgentID, message)
			}
		}
		borrowed.options.BackgroundTasks = &copy
	}
	return &borrowed, nil
}
