package resumestate

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (service *Service) BindOwnedFileHistory(copy *filehistorycopy.Service) error {
	if service == nil || copy == nil || service.ownedFileHistory != nil {
		return cooperative.ErrRestricted
	}
	service.ownedFileHistory = copy
	return nil
}

// BeginCopy is the real exported helper seam. Full load/activate requires its
// own plan/SessionStart await producer and is not emulated by fixture steps.
func (service *Service) BeginCopy(scope *cooperative.Scope, log *filehistorycopy.Log) (*ownerasync.OwnerFuture[struct{}], error) {
	if service == nil || service.ownedFileHistory == nil {
		return nil, cooperative.ErrRestricted
	}
	return service.ownedFileHistory.Begin(scope, log)
}

func (service *Service) DiagnosticForCopy(kind, name string, fields json.RawMessage) {
	service.diagnostic(kind, name, fields)
}
