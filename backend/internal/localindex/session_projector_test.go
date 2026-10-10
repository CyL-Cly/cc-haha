package localindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func sessionProjectorFixture(t *testing.T, threshold int) (*SessionProjector, *Database, string) {
	t.Helper()
	home := disposableHome(t)
	db, e := OpenDatabase(context.Background(), DatabaseOptions{Path: filepath.Join(home, "native-session.sqlite"), Kind: Primary})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	index, _ := NewSessionIndex(db)
	activity, _ := NewActivityIndex(db, time.UTC, true)
	p, e := NewSessionProjector(SessionProjectorOptions{Database: db, Index: index, Activity: activity, Scope: "fixture", Budget: sessionstore.NewBudget(), Reduction: reductionOptions(), SourceMetadata: func(path string) (SourceMetadata, error) {
		_, e := os.Stat(path)
		return SourceMetadata{"2026-10-01T00:00:00.000Z", "2026-10-06T00:00:00.000Z"}, e
	}, Now: func() time.Time { return time.UnixMilli(42) }, RecordStreamingThreshold: threshold})
	if e != nil {
		t.Fatal(e)
	}
	return p, db, home
}
func reduceSessionProject(v any, home string) any {
	var value any
	_ = json.Unmarshal(raw(v), &value)
	var walk func(any) any
	walk = func(v any) any {
		switch value := v.(type) {
		case []any:
			out := []any{}
			for _, v := range value {
				out = append(out, walk(v))
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, v := range value {
				switch k {
				case "fingerprint", "prefix_hash", "file_identity", "mtime_ms", "sourceFingerprint":
					continue
				}
				out[k] = walk(v)
			}
			return out
		case string:
			return strings.ReplaceAll(value, home, "$HOME")
		default:
			return value
		}
	}
	return walk(value)
}
func TestSessionProjectorActualSourceFullAppendAndAtomicTables(t *testing.T) {
	user := func(id, text string) string {
		return string(raw(map[string]any{"type": "user", "uuid": id, "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"role": "user", "content": text}})) + "\n"
	}
	assistant := string(raw(map[string]any{"type": "assistant", "uuid": "a1", "requestId": "r1", "version": "2.1.3", "sessionId": "session", "timestamp": "2026-10-06T00:01:00Z", "message": map[string]any{"id": "a1", "role": "assistant", "model": "claude-sonnet-5", "usage": map[string]any{"input_tokens": 123, "output_tokens": 9}, "content": []any{map[string]any{"type": "text", "text": "completed"}, map[string]any{"type": "tool_use", "name": "Skill", "input": map[string]any{"skill": "fixture"}}}}})) + "\n"
	ops := []map[string]any{{"write": user("u1", "first task") + assistant + strings.TrimSuffix(user("u2", "tail"), "\n")}, {}, {"append": "\n" + user("u3", "append")}, {}, {"write": `{"type":"custom-title","customTitle":123}` + "\n" + user("u4", "rewrite")}, {"path": "worker.jsonl", "activity": true, "write": assistant}, {"path": "worker.jsonl", "activity": true}, {"path": "worker.jsonl", "activity": true, "append": assistant}, {"path": "worker.jsonl", "activity": true, "remove": true}, {"remove": true}, {"delete": true}, {"write": user("u5", "new file")}, {"aborted": true, "append": user("u6", "not committed")}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "sessionProject", "operations": ops, "fixedMetadata": true})
	var rows []json.RawMessage
	_ = json.Unmarshal(source["observations"], &rows)
	p, db, home := sessionProjectorFixture(t, MaxProjectionRecordBytes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for n, row := range ops {
		path := "source.jsonl"
		if s, ok := row["path"].(string); ok {
			path = s
		}
		path = filepath.Join(home, path)
		if text, ok := row["write"].(string); ok {
			_ = os.WriteFile(path, []byte(text), 0600)
		}
		if text, ok := row["append"].(string); ok {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
		if row["remove"] == true {
			_ = os.Remove(path)
		}
		if row["aborted"] == true {
			cancel()
		}
		candidate := SessionCandidate{path, "session", "project", "2026-10-01T00:00:00Z", "2026-10-06T00:00:00Z", stringPointer("/fallback"), 42}
		var result SessionProjectResult
		var e error
		if row["activity"] == true {
			result, e = p.ProjectActivity(ctx, candidate)
		} else if row["delete"] == true {
			result, e = p.Delete(ctx, path, nil)
		} else {
			result, e = p.Project(ctx, candidate, nil)
		}
		tables := map[string]any{}
		if e == nil {
			e = db.Read(context.Background(), func(op *Operation) error {
				for _, table := range append([]string{"source_files", "sessions", "session_entries", "backfill_state"}, indexTableNames()[:6]...) {
					rows, e := op.All("SELECT * FROM " + table + " ORDER BY 1,2")
					if e != nil {
						return e
					}
					tables[table] = rows
				}
				return nil
			})
		}
		observation := map[string]any{"result": result, "tables": tables}
		if e != nil {
			var code any
			if c, ok := e.(*Error); ok {
				code = c.Code
			}
			observation = map[string]any{"error": e.Error(), "code": code}
		}
		if !reflect.DeepEqual(reduceSessionProject(observation, home), decoded(rows[n])) {
			t.Fatalf("Session projection row %d %s\nNative=%s\nSource=%s", n, raw(row), raw(reduceSessionProject(observation, home)), rows[n])
		}
	}
}
func TestSessionProjectorActualSourceOversizedSkeletonAndRetainedLimits(t *testing.T) {
	large := `{"type":"user","uuid":"real-large","message":{"role":"user","content":"` + strings.Repeat("preview body ", 25000) + `"}}` + "\n"
	image := `{"type":"assistant","uuid":"image","message":{"role":"assistant","model":"deepseek-flash","usage":{"input_tokens":12},"content":[{"type":"image","source":{"data":"` + strings.Repeat("A", 600000) + `"}},{"type":"tool_use","name":"Skill","input":{"skill":"actual-skill","command":"` + strings.Repeat("command ", 6000) + `"}}]}}` + "\n"
	ops := []map[string]any{{"write": large + image}, {}, {"append": `{"type":"user","runtimeModelId":"` + strings.Repeat("model", 900) + `"}` + "\n"}, {"write": large + `{"type":"assistant","unused":["` + strings.Repeat("body", 20000) + `"] invalid}` + "\n"}, {"append": `{"type":"user","message":{"content":"` + strings.Repeat("pending ", 18000)}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "sessionProject", "operations": ops, "fixedMetadata": true, "threshold": 512})
	var rows []json.RawMessage
	_ = json.Unmarshal(source["observations"], &rows)
	p, db, home := sessionProjectorFixture(t, 512)
	ctx := context.Background()
	path := filepath.Join(home, "source.jsonl")
	for n, row := range ops {
		if value, ok := row["write"].(string); ok {
			_ = os.WriteFile(path, []byte(value), 0600)
		}
		if value, ok := row["append"].(string); ok {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = f.WriteString(value)
			_ = f.Close()
		}
		result, e := p.Project(ctx, SessionCandidate{path, "session", "project", "2026-10-01T00:00:00Z", "2026-10-06T00:00:00Z", stringPointer("/fallback"), 42}, nil)
		observed := map[string]any{"result": result}
		if e != nil {
			observed = map[string]any{"error": e.Error(), "code": errorCode(e, "")}
		} else {
			tables := map[string]any{}
			e = db.Read(ctx, func(op *Operation) error {
				for _, table := range append([]string{"source_files", "sessions", "session_entries", "backfill_state"}, indexTableNames()[:6]...) {
					rows, e := op.All("SELECT * FROM " + table + " ORDER BY 1,2")
					if e != nil {
						return e
					}
					tables[table] = rows
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			observed["tables"] = tables
		}
		if !reflect.DeepEqual(reduceSessionProject(observed, home), decoded(rows[n])) {
			t.Fatalf("Oversized Source case %d\nNative=%s\nSource=%s", n, raw(reduceSessionProject(observed, home)), rows[n])
		}
	}
}
func TestSessionProjectionCommitFailureRollsBackEveryProjection(t *testing.T) {
	p, db, home := sessionProjectorFixture(t, 512)
	path := filepath.Join(home, "source.jsonl")
	_ = os.WriteFile(path, []byte(`{"type":"user","message":{"content":"atomic task"}}`+"\n"), 0600)
	p.o.BeforeSessionUpsert = func() { p.o.CanCommit = func() bool { return false } }
	result, e := p.Project(context.Background(), SessionCandidate{path, "session", "project", "2026-10-01T00:00:00Z", "2026-10-06T00:00:00Z", nil, 42}, nil)
	if e != nil || result.Kind != "retry" {
		t.Fatalf("Canceled commit %v %+v", e, result)
	}
	e = db.Read(context.Background(), func(op *Operation) error {
		for _, table := range append([]string{"source_files", "sessions", "session_entries"}, indexTableNames()[:6]...) {
			row, e := op.Get("SELECT count(*) AS count FROM " + table)
			if e != nil {
				return e
			}
			if intValue(row.Get("count")) != 0 {
				t.Fatalf("Partial transaction leaked into %s", table)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
