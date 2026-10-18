package modelaccounting

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PersistedCostFields is saveCurrentSessionCosts' projection of this ledger.
// The host merges these fields into the original project's existing config.
// It intentionally excludes model context-window metadata and undefined FPS.
func (s *Session) PersistedCostFields(scope *cooperative.Scope, sessionID string, p CostPresentation) (protocol.JSONObject, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) || p.NowMillis == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	start := p.StartMillis
	if s.restoredStartMillis != nil {
		start = *s.restoredStartMillis
	}
	models := protocol.NewJSONObject()
	search := 0.0
	for _, property := range s.models.OwnProperties() {
		usage := object(property.Value)
		row := protocol.NewJSONObject()
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
			row.Set(key, usage.Get(key))
		}
		if err := models.SetPropertyJSON(property.NameJSON, raw(row)); err != nil {
			return protocol.JSONObject{}, err
		}
		search += num(usage.Get("webSearchRequests"))
	}
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "lastCost", Value: raw(s.cost)},
		protocol.JSONField{Name: "lastAPIDuration", Value: raw(s.apiDuration)},
		protocol.JSONField{Name: "lastAPIDurationWithoutRetries", Value: raw(s.apiDurationWithoutRetries)},
		protocol.JSONField{Name: "lastDecodeDuration", Value: raw(s.generationDecodeDuration)},
		protocol.JSONField{Name: "lastTtftDuration", Value: raw(s.generationTtftDuration)},
		protocol.JSONField{Name: "lastToolDuration", Value: raw(s.restoredToolDuration)},
		protocol.JSONField{Name: "lastDuration", Value: raw(p.NowMillis() - start)},
		protocol.JSONField{Name: "lastLinesAdded", Value: raw(p.LinesAdded + s.restoredLinesAdded)},
		protocol.JSONField{Name: "lastLinesRemoved", Value: raw(p.LinesRemoved + s.restoredLinesRemoved)},
		protocol.JSONField{Name: "lastTotalInputTokens", Value: s.total.Get("input_tokens")},
		protocol.JSONField{Name: "lastTotalOutputTokens", Value: s.total.Get("output_tokens")},
		protocol.JSONField{Name: "lastTotalCacheCreationInputTokens", Value: s.total.Get("cache_creation_input_tokens")},
		protocol.JSONField{Name: "lastTotalCacheReadInputTokens", Value: s.total.Get("cache_read_input_tokens")},
		protocol.JSONField{Name: "lastTotalWebSearchRequests", Value: raw(search)},
		protocol.JSONField{Name: "lastModelUsage", Value: raw(models)},
		protocol.JSONField{Name: "lastSessionId", Value: quote(sessionID)},
	), nil
}

// ResetForTerminalSwitch ports resetCostState on the existing ledger. Pending
// attempts retain their original references, so their later finally reactions
// add to this reset state exactly as the process singleton does in Source.
func (s *Session) ResetForTerminalSwitch(scope *cooperative.Scope, now func() float64) error {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) || now == nil {
		return cooperative.ErrRestricted
	}
	s.cost = 0
	s.apiDuration, s.apiDurationWithoutRetries = 0, 0
	s.restoredToolDuration = 0
	s.generationDecodeDuration, s.generationTtftDuration = 0, 0
	start := now()
	s.restoredStartMillis = &start
	s.restoredLinesAdded, s.restoredLinesRemoved = 0, 0
	s.unknownModelCost = false
	s.models = protocol.NewJSONObject()
	s.total = ZeroUsage()
	return nil
}
