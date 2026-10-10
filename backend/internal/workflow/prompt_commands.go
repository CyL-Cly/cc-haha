package workflow

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func (s *Service) CommandsOnOwner(scope *cooperative.Scope) ([]*workflowcommands.Command, error) {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if s.definitions.BeginCommands == nil {
		return nil, &CapabilityError{"actual Workflow command source"}
	}
	future, e := s.definitions.BeginCommands(scope, s.definitions.OriginalCWD)
	if e != nil {
		return nil, e
	}
	commands, e := future.Await(scope)
	if e != nil {
		return nil, e
	}
	result := []*workflowcommands.Command{}
	for _, command := range commands {
		enabled, e := command.IsEnabled(scope)
		if e != nil {
			return nil, e
		}
		if enabled {
			result = append(result, command)
		}
	}
	return result, nil
}

func (s *Service) ClearCommandsOnOwner(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return cooperative.ErrRestricted
	}
	if s.definitions.ClearCommandCache == nil {
		return &CapabilityError{"actual Workflow command cache invalidation"}
	}
	return s.definitions.ClearCommandCache(scope)
}
