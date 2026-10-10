package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
)

// PermissionTypeError is a language property-access failure, distinct from
// synchronous getter errors and original rejected Promise reasons.
type PermissionTypeError struct{ Operation string }

func (e *PermissionTypeError) Error() string {
	return "null is not an object (evaluating permissionContext." + e.Operation + ")"
}
func (e *PermissionTypeError) Name() string { return "TypeError" }
func (a *DurationAttempt) RegisterQuery(scope *cooperative.Scope, input providerlogging.QueryInput) error {
	if a == nil || !scope.BelongsTo(a.session.owner) {
		return cooperative.ErrRestricted
	}
	if a.queryRegistered {
		return cooperative.ErrRestricted
	}
	a.queryRegistered = true
	permission, err := a.original.GetPermission(scope)
	if err != nil {
		return err
	}
	if permission == nil {
		return &PermissionTypeError{Operation: "then"}
	}
	queue := a.original.QueryLog
	if queue == nil {
		queue = a.session.queryLog
	}
	if !queue.Owns(a.session.owner) {
		return cooperative.ErrRestricted
	}
	child, err := permission.Then(scope, func(next *cooperative.Scope, context protocol.JSONObject, rejected error) error {
		if rejected != nil {
			return rejected
		}
		if !context.IsObject() {
			return &PermissionTypeError{Operation: "mode"}
		}
		return queue.LogQuery(next, input, context.Get("mode"))
	})
	if err == nil && a.original.Ignored != nil {
		err = ownerasync.Ignore(scope, child, a.original.Ignored, permission.Identity(), "query")
	}
	return err
}
func (s *Session) QueryLogSnapshot(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	return s.queryLog.Events(scope)
}
