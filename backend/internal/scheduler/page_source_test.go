package scheduler

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func pageOptions(v map[string]any) PageOptions {
	var o PageOptions
	for key, val := range v {
		switch key {
		case "taskId":
			o.TaskID = val.(string)
		case "limit":
			n := val.(float64)
			o.Limit = &n
		case "cursor":
			o.Cursor = val.(string)
		case "summaryOnly":
			o.SummaryOnly = val.(bool)
		case "nonterminalOnly":
			o.NonterminalOnly = val.(bool)
		case "completedAfterMs":
			n := val.(float64)
			o.CompletedAfterMs = &n
		}
	}
	return o
}
func objects(v any) []protocol.JSONObject {
	var rows []protocol.JSONObject
	_ = json.Unmarshal(raw(v), &rows)
	return rows
}
func TestSchedulerPaginationActualSourceScopeSummaryCursorAndIndexTransactions(t *testing.T) {
	runs := objects([]map[string]any{
		{"id": "a", "taskId": "task", "startedAt": "2026-10-07T01:00:00.000Z", "status": "running", "prompt": "private a", "output": ""},
		{"id": "b", "taskId": "task", "startedAt": "2026-10-07T01:00:00.000Z", "completedAt": "2026-10-07T02:00:00.000Z", "status": "completed", "prompt": "private b", "output": "answer", "exitCode": 0, "durationMs": 100, "sessionId": "sid"},
		{"id": "c", "taskId": "other", "startedAt": "2026-10-06T01:00:00.000Z", "status": "failed", "error": "failure"},
		{"id": "d", "taskId": "task", "startedAt": "bad", "status": "timeout", "output": nil, "error": 5},
	})
	rows := []map[string]any{}
	options := []map[string]any{}
	for _, limit := range []float64{-5, 0, 1, 2, 50} {
		for _, summary := range []bool{false, true} {
			for _, nonterminal := range []bool{false, true} {
				options = append(options, map[string]any{"limit": limit, "summaryOnly": summary, "nonterminalOnly": nonterminal})
			}
		}
	}
	options = append(options, map[string]any{"taskId": "task", "completedAfterMs": float64(1791324000000), "summaryOnly": true}, map[string]any{"cursor": "bad", "limit": float64(2)})
	first, e := Paginate(runs, PageOptions{Limit: ptrFloat(1)}, "revision")
	if e != nil {
		t.Fatal(e)
	}
	options = append(options, map[string]any{"cursor": first.NextCursor, "limit": float64(2)}, map[string]any{"cursor": first.NextCursor, "taskId": "other", "limit": float64(2)})
	for _, o := range options {
		rows = append(rows, map[string]any{"runs": runs, "options": o, "revision": "revision"})
	}
	indexRows := []map[string]any{{"replace": map[string]any{"runs": runs, "source": map[string]any{"path": "private-source", "size": 123, "mtimeMs": 100, "fingerprint": "snapshot"}}}, {"options": map[string]any{"limit": float64(1), "summaryOnly": true}}, {"replace": map[string]any{"runs": runs[:2], "source": map[string]any{"path": "failed", "size": 5, "mtimeMs": 500, "fingerprint": "must-rollback"}, "failAfterRows": 1}}, {"options": map[string]any{"limit": float64(100)}}}
	data := actualSource(t, "page", map[string]any{"rows": rows, "indexRows": indexRows})
	var source struct {
		Rows      []json.RawMessage
		IndexRows []struct {
			Value json.RawMessage
			Error string
		}
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	for n, o := range options {
		page, e := Paginate(runs, pageOptions(o), "revision")
		if e != nil || !equalJSON(raw(page), source.Rows[n]) {
			t.Errorf("page%d native%s Source%s error%v", n, raw(page), source.Rows[n], e)
		}
	}
	index, e := openRunIndex(t.TempDir(), func() int64 { return 1000 })
	if e != nil {
		t.Fatal(e)
	}
	defer index.db.Close()
	f := localindex.Fingerprint{Size: 123, MtimeMs: 100}
	if e = index.replace(context.Background(), runs, "private-source", f, "snapshot", 0); e != nil {
		t.Fatal(e)
	}
	one, e := index.page(context.Background(), PageOptions{Limit: ptrFloat(1), SummaryOnly: true})
	if e != nil || !equalJSON(raw(one), source.IndexRows[1].Value) {
		t.Fatal("SQL page differs", string(raw(one)), string(source.IndexRows[1].Value), e)
	}
	if e = index.replace(context.Background(), runs[:2], "failed", localindex.Fingerprint{Size: 5, MtimeMs: 500}, "must-rollback", 1); e == nil || e.Error() != source.IndexRows[2].Error {
		t.Fatal("SQL injected rollback", e)
	}
	after, e := index.page(context.Background(), PageOptions{Limit: ptrFloat(100)})
	if e != nil || !equalJSON(raw(after), source.IndexRows[3].Value) {
		t.Fatal("SQL rollback lost canonical projection", string(raw(after)), string(source.IndexRows[3].Value), e)
	}
	nan, e := Paginate(runs, PageOptions{Limit: ptrFloat(math.NaN())}, "memory")
	if e != nil || len(nan.Runs) != 0 || nan.NextCursor != "" {
		t.Fatal("JS NaN limit", nan, e)
	}
}
func ptrFloat(v float64) *float64 { return &v }
func equalJSON(a, b []byte) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
