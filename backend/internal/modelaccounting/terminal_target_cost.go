package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalTargetCosts captures getStoredSessionCosts' real result before the
// old session's save overwrites the one project config. No caller can supply a
// prepared restore value; the token binds the original ledger and resolver.
type TerminalTargetCosts struct {
	ledger *Session
	data   *restoredCosts
}

func (s *Session) CaptureTerminalTargetCosts(scope *cooperative.Scope, sessionID string, project *protocol.JSONObject, metadata RestoredModelResolver) (*TerminalTargetCosts, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	data, err := captureRestoredCosts(sessionID, project, metadata)
	if err != nil || data == nil {
		return nil, err
	}
	return &TerminalTargetCosts{ledger: s, data: data}, nil
}

func (s *Session) RestoreTerminalTargetCosts(scope *cooperative.Scope, target *TerminalTargetCosts, now func() float64) (bool, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) || target != nil && target.ledger != s {
		return false, cooperative.ErrRestricted
	}
	if target == nil {
		return false, nil
	}
	return s.applyRestoredCosts(scope, target.data, now)
}
