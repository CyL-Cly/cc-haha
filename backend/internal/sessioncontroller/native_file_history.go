package sessioncontroller

import (
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BindNativeFileHistory borrows the constructor's original Sink and AppState.
// Creating a second Writer/Host or replacing either state is not permitted.
func (c *Controller) BindNativeFileHistory(scope *cooperative.Scope, enabled func(*cooperative.Scope) (bool, error), now func() time.Time, uuid func() (string, error), files filehistory.Files) (*filehistory.Service, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || c.fileHistorySink == nil || c.nativeFileHistory != nil || !c.options.NativeTools {
		return nil, cooperative.ErrRestricted
	}
	owner := c.options.Bootstrap.Owner()
	service, e := filehistory.New(filehistory.Options{Owner: owner, Binding: c.fileHistorySink.NativeCopyBinding(), Sink: c.fileHistorySink, Files: files, ConfigHome: c.options.ResumeOptions.ConfigDir, OriginalCWD: c.options.ResumeOptions.OriginalCWD, Enabled: enabled, SessionID: func(s *cooperative.Scope) (string, error) {
		if !s.BelongsTo(owner) {
			return "", cooperative.ErrRestricted
		}
		return c.SessionID(), nil
	}, GetState: func(s *cooperative.Scope) (protocol.JSONObject, error) {
		if !s.BelongsTo(owner) {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		return c.appState.FileHistory.Clone(), nil
	}, SetState: func(s *cooperative.Scope, state protocol.JSONObject) error {
		if !s.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		return s.Inline(cooperative.Read, func(*cooperative.Scope) error { c.appState.FileHistory = state; return nil })
	}, Now: now, UUID: uuid, OnError: c.options.ResumeOptions.OnError})
	if e != nil {
		return nil, e
	}
	c.nativeFileHistory = service
	return service, nil
}

func (c *Controller) PersistsOnOwner(scope *cooperative.Scope) (bool, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return false, cooperative.ErrRestricted
	}
	// QueryEngine captures !isSessionPersistenceDisabled(). The transcript
	// writer separately checks retention-zero, test and skip-history policies.
	return !c.writer.options.SessionPersistenceDisabled, nil
}

func (c *Controller) ReportFileHistoryError(e error) {
	if c != nil && e != nil {
		c.options.ResumeOptions.OnError(e)
	}
}

func (c *Controller) UsesNativeFileHistory(service *filehistory.Service) bool {
	return c != nil && service != nil && c.nativeFileHistory == service
}
