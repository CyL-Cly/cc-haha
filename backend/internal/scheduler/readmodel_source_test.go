package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestSchedulerReadModelActualSourceWarmPollRewriteOffFullAndBlankFallback(t *testing.T) {
	first := `{"runs":[{"id":"a","taskId":"task","taskName":"private name","prompt":"private prompt","output":"private output","startedAt":"2026-10-07T01:00:00Z","status":"completed"},{"id":"b","taskId":"task","startedAt":"2026-10-06T01:00:00Z","status":"running"}]}`
	second := `{"runs":[{"id":"c","taskId":"task","startedAt":"2026-10-07T03:00:00Z","status":"failed","error":"private failure"}]}`
	actions := []map[string]any{{"write": first, "mode": "on", "options": map[string]any{"summaryOnly": true}}, {"options": map[string]any{"summaryOnly": true}}, {"method": "full", "limit": float64(-1)}, {"write": second, "options": map[string]any{"summaryOnly": true}}, {"method": "detail", "id": "c"}, {"mode": "off", "options": map[string]any{"summaryOnly": true}}, {"mode": "on", "options": map[string]any{"summaryOnly": true}}, {"write": "\ufeff ", "options": map[string]any{"summaryOnly": true}}}
	data := actualSource(t, "readmodel", map[string]any{"actions": actions})
	var source struct {
		Rows []struct {
			Value   json.RawMessage
			Metrics struct{ CanonicalReadCount, RebuildCount int }
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	mode := "on"
	o := disposableOptions(t)
	o.Environment = func() map[string]string { return map[string]string{"CC_HAHA_LOCAL_INDEX": mode} }
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	for n, action := range actions {
		if v := action["write"]; v != nil {
			if e = os.WriteFile(s.store.runsPath(), []byte(v.(string)), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if v := action["mode"]; v != nil {
			mode = v.(string)
		}
		var value any
		switch action["method"] {
		case "full":
			value, e = s.GetRecentRuns(context.Background(), action["limit"].(float64))
		case "detail":
			value, e = s.GetRunDetail(context.Background(), action["id"].(string))
		default:
			value, e = s.GetRunsPage(context.Background(), pageOptions(action["options"].(map[string]any)))
		}
		if e != nil {
			t.Fatal(e)
		}
		var native, expected map[string]json.RawMessage
		actual := raw(value)
		want := source.Rows[n].Value
		if json.Unmarshal(actual, &native) == nil && json.Unmarshal(want, &expected) == nil {
			delete(native, "revisionToken")
			delete(expected, "revisionToken")
			actual = raw(native)
			want = raw(expected)
		}
		if !equalJSON(actual, want) {
			t.Errorf("poll%d Go%s Source%s", n, actual, want)
		}
		s.projection.mu.Lock()
		reads, rebuilds := s.projection.canonicalReads, s.projection.rebuilds
		s.projection.mu.Unlock()
		if reads != source.Rows[n].Metrics.CanonicalReadCount || rebuilds != source.Rows[n].Metrics.RebuildCount {
			t.Errorf("poll%d counters Go%d/%d Source%+v", n, reads, rebuilds, source.Rows[n].Metrics)
		}
	}
}
