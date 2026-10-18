package workflow

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

// BeginCommandsForDirectoryOnOwner starts/returns the existing cwd-keyed
// producer without awaiting it. Consumers can start other actual sources and
// attach Promise.all directly to this same retained operation.
func (s *Service) BeginCommandsForDirectoryOnOwner(scope *cooperative.Scope, cwd string) (*ownerasync.OwnerFuture[[]*workflowcommands.Command], error) {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if s.definitions.BeginCommands == nil {
		return nil, &CapabilityError{"actual Workflow command source"}
	}
	return s.definitions.BeginCommands(scope, cwd)
}
