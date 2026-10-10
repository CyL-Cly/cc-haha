package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AddGenerationDuration is the real QueryEngine message_stop consumer. It is
// separate from producer finally cost and success-reaction API duration.
func (s *Session) AddGenerationDuration(scope *cooperative.Scope, decode, ttft float64) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.generationDecodeDuration += decode
	s.generationTtftDuration += ttft
	return s.relayLedger(scope, LedgerDelta{kind: "generation", decode: decode, ttft: ttft})
}

// GenerationSnapshot preserves the old cost Snapshot fields/order unchanged.
func (s *Session) GenerationSnapshot(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "totalDecodeDuration", Value: raw(s.generationDecodeDuration)}, protocol.JSONField{Name: "totalTtftDuration", Value: raw(s.generationTtftDuration)}), nil
}
