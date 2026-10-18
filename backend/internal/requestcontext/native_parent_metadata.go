package requestcontext

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Source paramsFromContext calls getAPIMetadata for every creation attempt,
// independently of withRetry's cached client. Only an admitted native child can
// sample the process's current parent bootstrap authority here.
func (prepared *PreparedContext) RefreshNativeParentMetadataOnOwner(scope *cooperative.Scope, binding *bootstrapstate.NativeParentClientState) error {
	if prepared == nil || prepared.input.Host.Profile != NativeDirectRelayProfile || binding == nil {
		return cooperative.ErrRestricted
	}
	id, err := binding.SessionID(scope)
	if err != nil {
		return err
	}
	prepared.input.Host.SessionID = id
	return nil
}
