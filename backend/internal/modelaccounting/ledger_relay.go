package modelaccounting

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
)

// LedgerDelta comes only from an actual accounting producer after its charge
// or timing reaction. It contains values, never a foreign Scope, calculator,
// settings getter, or mutable ledger. Raw fields remain private to this package.
type LedgerDelta struct {
	kind, model                            string
	cost, including, without, decode, ttft float64
	usage, row                             protocol.JSONObject
	unknown                                bool
}

func (d LedgerDelta) Clone() LedgerDelta { d.usage = d.usage.Clone(); d.row = d.row.Clone(); return d }
func (s *Session) SetLedgerRelay(scope *cooperative.Scope, relay func(*cooperative.Scope, LedgerDelta) error) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if s.ledgerRelay != nil || relay == nil {
		return errors.New("Actual accounting relay is already bound or absent")
	}
	s.ledgerRelay = relay
	return nil
}
func (s *Session) relayLedger(scope *cooperative.Scope, delta LedgerDelta) error {
	if s.ledgerRelay == nil {
		return nil
	}
	return s.ledgerRelay(scope, delta.Clone())
}
func (s *Session) ApplyLedgerDelta(scope *cooperative.Scope, d LedgerDelta) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	switch d.kind {
	case "charge":
		if d.model == "" || !d.usage.IsObject() || !d.row.IsObject() || math.IsNaN(d.cost) || math.IsInf(d.cost, 0) || d.cost < 0 {
			return errors.New("Invalid actual accounting charge relay")
		}
		previous := object(s.models.Get(d.model))
		if !previous.IsObject() {
			previous = protocol.NewJSONObject()
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD", "contextWindow", "maxOutputTokens"} {
				previous.Set(key, raw(0))
			}
		}
		for _, mapping := range [][2]string{{"inputTokens", "input_tokens"}, {"outputTokens", "output_tokens"}, {"cacheReadInputTokens", "cache_read_input_tokens"}, {"cacheCreationInputTokens", "cache_creation_input_tokens"}} {
			previous.Set(mapping[0], raw(num(previous.Get(mapping[0]))+num(d.usage.Get(mapping[1]))))
		}
		search := num(object(d.usage.Get("server_tool_use")).Get("web_search_requests"))
		previous.Set("webSearchRequests", raw(num(previous.Get("webSearchRequests"))+search))
		previous.Set("costUSD", raw(num(previous.Get("costUSD"))+d.cost))
		previous.Set("contextWindow", d.row.Get("contextWindow"))
		previous.Set("maxOutputTokens", d.row.Get("maxOutputTokens"))
		s.models.Set(d.model, raw(previous))
		s.cost += d.cost
		s.total = AccumulateUsage(s.total, d.usage)
		s.unknownModelCost = s.unknownModelCost || d.unknown
	case "api_duration":
		s.apiDuration += d.including
		s.apiDurationWithoutRetries += d.without
	case "generation":
		s.generationDecodeDuration += d.decode
		s.generationTtftDuration += d.ttft
	default:
		return errors.New("Invalid accounting relay producer kind")
	}
	return s.relayLedger(scope, d)
}
