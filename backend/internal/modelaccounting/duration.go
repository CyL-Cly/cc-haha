package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
)

// SuccessDuration binds the original clock and permission Promise producer.
// The getter is called at the pre-query and normal-success Source callsites.
// The returned native Promise belongs to the same Owner; no logging is awaited.
// Telemetry beyond the two duration fields remains outside this finite seam.
type SuccessDuration struct {
	NowMillis     func() float64
	GetPermission func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error)
	Ignored       *ownerasync.IgnoredRejections
	QueryLog      *providerlogging.Queue
}
type DurationAttempt struct {
	session                      *Session
	original                     SuccessDuration
	startIncludingRetries, start float64
	registered                   bool
	queryRegistered              bool
}

func (s *Session) BeginDuration(scope *cooperative.Scope, original SuccessDuration) (*DurationAttempt, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	if original.NowMillis == nil || original.GetPermission == nil {
		return nil, unsupported("original success clock/permission Promise")
	}
	// Source reads each original primitive separately at claude.ts1661-1662.
	including := original.NowMillis()
	start := original.NowMillis()
	return &DurationAttempt{session: s, original: original, startIncludingRetries: including, start: start}, nil
}
func (a *DurationAttempt) StartAttempt(scope *cooperative.Scope) error {
	if a == nil || !scope.BelongsTo(a.session.owner) || a.registered {
		return cooperative.ErrRestricted
	}
	// Shared requestretry invokes OnAttempt after GetClient, before BuildAttempt.
	a.start = a.original.NowMillis()
	return nil
}
func (a *DurationAttempt) AttemptStartMillis(scope *cooperative.Scope) (float64, error) {
	if a == nil || !scope.BelongsTo(a.session.owner) {
		return 0, cooperative.ErrRestricted
	}
	return a.start, nil
}
func (a *DurationAttempt) RegisterSuccess(scope *cooperative.Scope) error {
	if a == nil || !scope.BelongsTo(a.session.owner) {
		return cooperative.ErrRestricted
	}
	if a.registered {
		return nil
	}
	a.registered = true
	permission, err := a.original.GetPermission(scope)
	if err != nil {
		return err
	}
	if permission == nil {
		return &PermissionTypeError{Operation: "then"}
	}
	child, err := permission.Then(scope, func(next *cooperative.Scope, context protocol.JSONObject, rejected error) error {
		if rejected != nil {
			return rejected
		} // Reject only the ignored dependent Promise.
		if !context.IsObject() {
			return &PermissionTypeError{Operation: "mode"}
		}
		// logging.ts686-688: two late Date.now reads, then += current singleton
		// fields. Do not capture ledger values or discard by activation generation.
		without := a.original.NowMillis() - a.start
		including := a.original.NowMillis() - a.startIncludingRetries
		return a.session.addDuration(next, including, without)
	})
	if err == nil && a.original.Ignored != nil {
		err = ownerasync.Ignore(scope, child, a.original.Ignored, permission.Identity(), "success")
	}
	return err
}
func (s *Session) addDuration(scope *cooperative.Scope, including, without float64) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.apiDuration += including
	s.apiDurationWithoutRetries += without
	return s.relayLedger(scope, LedgerDelta{kind: "api_duration", including: including, without: without})
}

// DurationSnapshot is separate so the established cost Snapshot byte contract
// remains unchanged. SDK reads including-retries; both fields have one authority.
func (s *Session) DurationSnapshot(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "totalAPIDuration", Value: raw(s.apiDuration)}, protocol.JSONField{Name: "totalAPIDurationWithoutRetries", Value: raw(s.apiDurationWithoutRetries)}), nil
}
