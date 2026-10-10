package modelaccounting

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// UsageSnapshot is Source getSessionUsageSnapshot over the sole live ledger.
// It reads original clock and tool counters; it does not rebuild or charge usage.
func (s *Session) UsageSnapshot(scope *cooperative.Scope, p CostPresentation) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) || p.NowMillis == nil || p.CanonicalName == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	start := p.StartMillis
	if s.restoredStartMillis != nil {
		start = *s.restoredStartMillis
	}
	models := []protocol.JSONObject{}
	search := 0.0
	for _, property := range s.models.OwnProperties() {
		var model string
		if err := json.Unmarshal(property.NameJSON, &model); err != nil {
			return protocol.JSONObject{}, err
		}
		name, err := p.CanonicalName(model)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		usage := object(property.Value)
		row := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quote(model)}, protocol.JSONField{Name: "displayName", Value: quote(name)})
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
			row.Set(key, usage.Get(key))
		}
		row.Set("costDisplay", quote(costText(num(usage.Get("costUSD")))))
		row.Set("contextWindow", usage.Get("contextWindow"))
		row.Set("maxOutputTokens", usage.Get("maxOutputTokens"))
		models = append(models, row)
		search += num(usage.Get("webSearchRequests"))
	}
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "totalCostUSD", Value: raw(s.cost)},
		protocol.JSONField{Name: "costDisplay", Value: quote(costText(s.cost))},
		protocol.JSONField{Name: "hasUnknownModelCost", Value: raw(s.unknownModelCost)},
		protocol.JSONField{Name: "totalAPIDuration", Value: raw(s.apiDuration)},
		protocol.JSONField{Name: "totalDecodeDuration", Value: raw(s.generationDecodeDuration)},
		protocol.JSONField{Name: "totalTtftDuration", Value: raw(s.generationTtftDuration)},
		protocol.JSONField{Name: "totalDuration", Value: raw(p.NowMillis() - start)},
		protocol.JSONField{Name: "totalLinesAdded", Value: raw(p.LinesAdded + s.restoredLinesAdded)},
		protocol.JSONField{Name: "totalLinesRemoved", Value: raw(p.LinesRemoved + s.restoredLinesRemoved)},
		protocol.JSONField{Name: "totalInputTokens", Value: s.total.Get("input_tokens")},
		protocol.JSONField{Name: "totalOutputTokens", Value: s.total.Get("output_tokens")},
		protocol.JSONField{Name: "totalCacheReadInputTokens", Value: s.total.Get("cache_read_input_tokens")},
		protocol.JSONField{Name: "totalCacheCreationInputTokens", Value: s.total.Get("cache_creation_input_tokens")},
		protocol.JSONField{Name: "totalWebSearchRequests", Value: raw(search)},
		protocol.JSONField{Name: "models", Value: raw(models)},
	), nil
}
