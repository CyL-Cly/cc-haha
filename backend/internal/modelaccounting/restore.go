package modelaccounting

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
)

// RestoreCostStateForSession consumes original persisted project data. It does
// not derive a cost from transcript usage or clear the ledger on SID mismatch.
func (s *Session) RestoreCostStateForSession(scope *cooperative.Scope, sessionID string, project *protocol.JSONObject, metadata RestoredModelResolver, now func() float64) (bool, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return false, cooperative.ErrRestricted
	}
	if project == nil || !project.IsObject() {
		return false, unsupported("original project cost config object")
	}
	var storedSID string
	if json.Unmarshal(project.Get("lastSessionId"), &storedSID) != nil || storedSID != sessionID {
		return false, nil
	}
	keys := []string{"lastCost", "lastAPIDuration", "lastAPIDurationWithoutRetries", "lastDecodeDuration", "lastTtftDuration", "lastToolDuration", "lastLinesAdded", "lastLinesRemoved"}
	values := make([]float64, len(keys))
	for i, key := range keys {
		v, e := storedNumber(project.Get(key), key)
		if e != nil {
			return false, e
		}
		values[i] = v
	}
	for _, i := range []int{6, 7} {
		if values[i] != math.Trunc(values[i]) || values[i] > float64(math.MaxInt32) {
			return false, unsupported("stored line counter finite integer domain")
		}
	}
	var models *protocol.JSONObject
	if v := project.Get("lastModelUsage"); storedTruthy(v) {
		var original protocol.JSONObject
		if original.UnmarshalJSON(v) != nil || !original.IsObject() {
			return false, unsupported("stored lastModelUsage object")
		}
		copy := protocol.NewJSONObject()
		for _, property := range original.OwnProperties() {
			var name string
			if json.Unmarshal(property.NameJSON, &name) != nil {
				return false, unsupported("stored model name")
			}
			var usage protocol.JSONObject
			if usage.UnmarshalJSON(property.Value) != nil || !usage.IsObject() {
				return false, unsupported("stored model usage spread domain")
			}
			// Unknown row properties retain their original JSON and enumeration order.
			for _, field := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
				if _, e := storedNumber(usage.Get(field), field); e != nil {
					return false, e
				}
			}
			contextWindow, outputTokens, e := metadata.Resolve(name)
			if e != nil {
				return false, e
			}
			usage.Set("contextWindow", raw(contextWindow))
			usage.Set("maxOutputTokens", raw(outputTokens))
			b, e := usage.MarshalJSON()
			if e != nil {
				return false, e
			}
			copy.Set(name, b)
		}
		models = &copy
	}
	var start *float64
	if v := project.Get("lastDuration"); storedTruthy(v) {
		duration, e := storedNumber(v, "lastDuration")
		if e != nil {
			return false, e
		}
		if now == nil {
			return false, unsupported("actual restore Date.now primitive")
		}
		t := now() - duration
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return false, unsupported("actual restore Date.now finite domain")
		}
		start = &t
	}
	// Source setCostStateForRestore order. No unknown-cost reset and no save/flush.
	s.cost = values[0]
	s.apiDuration = values[1]
	s.apiDurationWithoutRetries = values[2]
	s.generationDecodeDuration = values[3]
	s.generationTtftDuration = values[4]
	s.restoredToolDuration = values[5]
	s.restoredLinesAdded = int(values[6])
	s.restoredLinesRemoved = int(values[7])
	if models != nil {
		s.models = *models
		// Source totals are getters over modelUsage, never persisted lastTotal*.
		total := ZeroUsage()
		for _, p := range s.models.OwnProperties() {
			row := object(p.Value)
			for _, k := range [][2]string{{"input_tokens", "inputTokens"}, {"output_tokens", "outputTokens"}, {"cache_read_input_tokens", "cacheReadInputTokens"}, {"cache_creation_input_tokens", "cacheCreationInputTokens"}} {
				total.Set(k[0], raw(num(total.Get(k[0]))+num(row.Get(k[1]))))
			}
			server := object(total.Get("server_tool_use"))
			server.Set("web_search_requests", raw(num(server.Get("web_search_requests"))+num(row.Get("webSearchRequests"))))
			total.Set("server_tool_use", raw(server))
		}
		s.total = total
	}
	if start != nil {
		s.restoredStartMillis = start
	}
	return true, nil
}
func storedNumber(value json.RawMessage, field string) (float64, error) {
	if value == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return 0, nil
	}
	var n float64
	if json.Unmarshal(value, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, unsupported("stored numeric cost domain " + field)
	}
	return n, nil
}
func storedTruthy(value json.RawMessage) bool {
	var v any
	if json.Unmarshal(value, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	default:
		return true
	}
}
