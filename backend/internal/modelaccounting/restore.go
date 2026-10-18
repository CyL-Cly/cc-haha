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
type restoredCosts struct {
	values   []float64
	models   *protocol.JSONObject
	duration *float64
}

func captureRestoredCosts(sessionID string, project *protocol.JSONObject, metadata RestoredModelResolver) (*restoredCosts, error) {
	if project == nil || !project.IsObject() {
		return nil, unsupported("original project cost config object")
	}
	var storedSID string
	if json.Unmarshal(project.Get("lastSessionId"), &storedSID) != nil || storedSID != sessionID {
		return nil, nil
	}
	keys := []string{"lastCost", "lastAPIDuration", "lastAPIDurationWithoutRetries", "lastDecodeDuration", "lastTtftDuration", "lastToolDuration", "lastLinesAdded", "lastLinesRemoved"}
	values := make([]float64, len(keys))
	for i, key := range keys {
		v, e := storedNumber(project.Get(key), key)
		if e != nil {
			return nil, e
		}
		values[i] = v
	}
	for _, i := range []int{6, 7} {
		if values[i] != math.Trunc(values[i]) || values[i] > float64(math.MaxInt32) {
			return nil, unsupported("stored line counter finite integer domain")
		}
	}
	var models *protocol.JSONObject
	if v := project.Get("lastModelUsage"); storedTruthy(v) {
		var original protocol.JSONObject
		if original.UnmarshalJSON(v) != nil || !original.IsObject() {
			return nil, unsupported("stored lastModelUsage object")
		}
		copy := protocol.NewJSONObject()
		for _, property := range original.OwnProperties() {
			var name string
			if json.Unmarshal(property.NameJSON, &name) != nil {
				return nil, unsupported("stored model name")
			}
			var usage protocol.JSONObject
			if usage.UnmarshalJSON(property.Value) != nil || !usage.IsObject() {
				return nil, unsupported("stored model usage spread domain")
			}
			// Unknown row properties retain their original JSON and enumeration order.
			for _, field := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
				if _, e := storedNumber(usage.Get(field), field); e != nil {
					return nil, e
				}
			}
			contextWindow, outputTokens, e := metadata.Resolve(name)
			if e != nil {
				return nil, e
			}
			usage.Set("contextWindow", raw(contextWindow))
			usage.Set("maxOutputTokens", raw(outputTokens))
			b, e := usage.MarshalJSON()
			if e != nil {
				return nil, e
			}
			copy.Set(name, b)
		}
		models = &copy
	}
	var duration *float64
	if value := project.Get("lastDuration"); storedTruthy(value) {
		d, err := storedNumber(value, "lastDuration")
		if err != nil {
			return nil, err
		}
		duration = &d
	}
	return &restoredCosts{values: values, models: models, duration: duration}, nil
}

func (s *Session) applyRestoredCosts(scope *cooperative.Scope, data *restoredCosts, now func() float64) (bool, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) {
		return false, cooperative.ErrRestricted
	}
	if data == nil {
		return false, nil
	}
	var start *float64
	if data.duration != nil {
		if now == nil {
			return false, unsupported("actual restore Date.now primitive")
		}
		t := now() - *data.duration
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return false, unsupported("actual restore Date.now finite domain")
		}
		start = &t
	}
	// Source setCostStateForRestore order. No unknown-cost reset and no save/flush.
	values, models := data.values, data.models
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

func (s *Session) RestoreCostStateForSession(scope *cooperative.Scope, sessionID string, project *protocol.JSONObject, metadata RestoredModelResolver, now func() float64) (bool, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.owner) {
		return false, cooperative.ErrRestricted
	}
	data, err := captureRestoredCosts(sessionID, project, metadata)
	if err != nil {
		return false, err
	}
	return s.applyRestoredCosts(scope, data, now)
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
