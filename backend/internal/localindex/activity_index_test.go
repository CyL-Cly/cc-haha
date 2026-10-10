package localindex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActivityIndexActualSourceProjectionTablesAndDateAggregation(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ops := []map[string]any{{"method": "aggregate", "range": "all", "now": now.Format(time.RFC3339)}, {"method": "count"}, {"method": "source", "path": "missing"}}
	for n := 0; n < 5; n++ {
		sub := n == 1
		parent := []string{"owner", "owner", "second", "stale", "future"}[n]
		dates := []string{"2026-10-01", "2026-10-06", "2026-10-06", "2026-09-01", "2026-10-07"}
		date := dates[n]
		first, last := date+"T01:00:00Z", date+"T01:03:00Z"
		hour := 1
		shot := n%3 + 1
		model := []string{"claude-sonnet-5", "deepseek-flash", "claude-opus-5-5", "claude-sonnet-5", "deepseek-flash"}[n]
		day := ActivityDay{Date: date, MessageCount: 3, ToolCallCount: 2}
		if sub {
			day.MessageCount = 0
		}
		fields := rowObject(protocol.JSONField{Name: "date", Value: raw(date)}, protocol.JSONField{Name: "model", Value: raw(model)})
		for _, f := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "contextWindow", "maxOutputTokens"} {
			fields.Set(f, raw(0))
		}
		fields.Set("inputTokens", raw(100*(n+1)))
		fields.Set("outputTokens", raw(20))
		activity := ActivityProjection{IsSubagent: sub, FirstTimestamp: &first, LastTimestamp: &last, ActiveDurationMs: 60000, MessageCount: 3, StartHour: &hour, SpeculationTimeSavedMs: float64(n+1) * 100, ShotCount: &shot, Daily: []ActivityDay{day}, Models: []ActivityModel{{Fields: fields, CostUSD: float64(n+1) * 0.001}}, Tools: []NamedUsage{{date, "Bash", 2}}, Skills: []NamedUsage{{date, "fixture-skill", 1}}}
		mtime := float64(now.UnixMilli())
		if n == 0 || n == 3 {
			mtime = float64(now.AddDate(0, 0, -40).UnixMilli())
		}
		source := ActivitySourceDescriptor{Path: "/p/" + parent + string(rune('0'+n)) + ".jsonl", ParentSessionID: parent, ProjectPath: "project", IsSubagent: sub, Fingerprint: Fingerprint{Size: 100, MtimeMs: mtime, IndexedBytes: 100, ParserVersion: 9}, FingerprintJSON: "fixture", IndexedBytes: 100, ParserVersion: 9, State: "ready", UpdatedAtMs: 42}
		ops = append(ops, map[string]any{"method": "write", "source": source, "activity": activity})
	}
	ops = append(ops, map[string]any{"method": "sources"}, map[string]any{"method": "count"}, map[string]any{"method": "source", "path": "/p/owner0.jsonl"}, map[string]any{"method": "backfillWrite", "state": ActivityBackfill{Scope: "fixture", State: "ready", Discovered: 5, Indexed: 5, UpdatedAtMs: 42}}, map[string]any{"method": "backfill", "scope": "fixture"}, map[string]any{"method": "backfill", "scope": "missing"}, map[string]any{"method": "snapshot"})
	for _, rangeName := range []string{"all", "7d", "30d"} {
		ops = append(ops, map[string]any{"method": "aggregate", "range": rangeName, "now": now.Format(time.RFC3339)}, map[string]any{"method": "aggregate", "range": rangeName, "now": now.AddDate(0, 0, 1).Format(time.RFC3339)})
	}
	for _, shots := range []bool{false, true} {
		t.Run(map[bool]string{false: "noShots", true: "withShots"}[shots], func(t *testing.T) {
			source := sourceIndex(t, disposableHome(t), map[string]any{"action": "activityIndex", "shots": shots, "operations": ops})
			var rows []json.RawMessage
			_ = json.Unmarshal(source["observations"], &rows)
			ctx := context.Background()
			db, e := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(t.TempDir(), "activity.sqlite"), Kind: Primary})
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			index, _ := NewActivityIndex(db, time.UTC, shots)
			dateParser := activitystatsapi.NewIndexDateParser(time.UTC)
			for n, row := range ops {
				var r struct {
					Method, Path, Scope, Range, Now string
					Source                          ActivitySourceDescriptor
					Activity                        ActivityProjection
					State                           ActivityBackfill
				}
				if e = json.Unmarshal(raw(row), &r); e != nil {
					t.Fatal(e)
				}
				var result any = "$undefined"
				switch r.Method {
				case "write":
					e = db.Transaction(ctx, func(op *Operation) error { return WriteActivityProjection(op, r.Source, r.Activity, dateParser.Parse) })
				case "backfillWrite":
					e = db.Transaction(ctx, func(op *Operation) error { return WriteActivityBackfill(op, r.State, nil) })
				case "source":
					result, e = index.GetSource(ctx, r.Path)
				case "sources":
					result, e = index.ListSources(ctx)
				case "count":
					result, e = index.CountSources(ctx)
				case "backfill":
					result, e = index.GetBackfill(ctx, r.Scope)
				case "aggregate":
					when, _ := time.Parse(time.RFC3339, r.Now)
					result, e = index.Aggregate(ctx, r.Range, when)
				case "snapshot":
					tables := map[string]any{}
					e = db.Read(ctx, func(op *Operation) error {
						for _, table := range indexTableNames() {
							rows, e := op.All("SELECT * FROM " + table + " ORDER BY 1,2")
							if e != nil {
								return e
							}
							tables[table] = rows
						}
						return nil
					})
					result = tables
				default:
					t.Fatal(r.Method)
				}
				observation := map[string]any{"result": result}
				if e != nil {
					observation = map[string]any{"error": e.Error()}
				}
				if !reflect.DeepEqual(decoded(observation), decoded(rows[n])) {
					t.Fatalf("Activity Source row %d %s\nNative=%s\nSource=%s", n, raw(row), raw(observation), rows[n])
				}
			}
		})
	}
}
