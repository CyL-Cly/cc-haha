package localindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func contentFixtureSource(path, owner, project string, modified float64) ContentSource {
	return ContentSource{Path: path, ProjectPath: project, OwnerSessionID: owner, OwnerTranscriptPath: owner + ".jsonl", ModifiedAtMs: modified, SizeBytes: 300, MtimeMs: modified, Fingerprint: "fixture", IndexedBytes: 300, IndexedLines: 3, ParserVersion: 1, State: "ready", UpdatedAtMs: 42}
}
func doc(line, segment int64, body string) ContentDocument {
	return ContentDocument{JSONLLine: line, ByteStart: (line - 1) * 100, ByteLength: 100, SegmentIndex: segment, Role: "user", Body: body, NormalizedBody: NormalizeContent(body)}
}
func TestContentIndexActualSourceDifferential(t *testing.T) {
	ops := []map[string]any{{"method": "query", "query": "needle"}, {"method": "suggest", "query": "needle"}, {"method": "query", "query": " \ufeff"}, {"method": "readiness"}}
	add := func(path, owner, project string, modified float64, documents []ContentDocument) {
		ops = append(ops, map[string]any{"method": "replace", "source": contentFixtureSource(path, owner, project, modified), "documents": documents})
	}
	add("/p/b.jsonl", "b", "p", 200, []ContentDocument{doc(1, 0, "中文全文检索 needle SQLite"), doc(2, 0, `literal .* [needle] A"B 100% x_y`), doc(3, 0, "ΟΣ Σ İ K K ẞ 😄猫咪猫")})
	add("/p/a.jsonl", "a", "p", 200, []ContentDocument{doc(1, 0, "needle older"), doc(1, 1, "needle second block")})
	add("/p/a/sub/worker.jsonl", "a", "p", 300, []ContentDocument{doc(1, 0, "needle nested owner grouped")})
	add("/z/c.jsonl", "c", "z", 100, []ContentDocument{doc(1, 0, "needle last session")})
	pending := contentFixtureSource("/p/pending.jsonl", "pending", "p", 400)
	pending.State = "pending"
	ops = append(ops, map[string]any{"method": "replace", "source": pending, "documents": []ContentDocument{doc(1, 0, "needle")}})
	ops = append(ops, map[string]any{"method": "list"}, map[string]any{"method": "count"}, map[string]any{"method": "get", "path": "/p/b.jsonl"}, map[string]any{"method": "setReadiness", "readiness": ContentReadinessWrite{State: "ready", Discovered: 5, Indexed: 4}}, map[string]any{"method": "readiness"})
	for _, query := range []string{"needle", "中文", "文", "SQLite", "sqlite", "A\"B", ".*", "[needle]", "100%", "x_y", "ΟΣ", "ος", "İ", "i\u0307", "K", "k", "ß", "😄猫咪", "猫", "no match", " \ufeffneedle\ufeff "} {
		for _, sensitive := range []bool{false, true} {
			ops = append(ops, map[string]any{"method": "query", "query": query, "options": map[string]any{"caseSensitive": sensitive}}, map[string]any{"method": "suggest", "query": query})
		}
	}
	for _, o := range []map[string]any{{"limit": 1, "matchesPerSession": 1}, {"limit": 0, "matchesPerSession": 0}, {"limit": 1.9, "matchesPerSession": 1.9}, {"limit": 101, "matchesPerSession": 30}, {"project": "p"}, {"project": "missing"}, {"modifiedAfterMs": 200}, {"modifiedBeforeMs": 200}, {"modifiedAfterMs": 201, "modifiedBeforeMs": 299}, {"limit": 2, "matchesPerSession": 2}} {
		ops = append(ops, map[string]any{"method": "query", "query": "needle", "options": o})
	}
	ops = append(ops, map[string]any{"method": "delete", "path": "/p/a/sub/worker.jsonl"}, map[string]any{"method": "query", "query": "needle"})
	ops = append(ops, map[string]any{"method": "append", "source": contentFixtureSource("/p/a.jsonl", "a", "p", 500), "documents": []ContentDocument{doc(2, 0, "needle appended")}}, map[string]any{"method": "query", "query": "needle"}, map[string]any{"method": "append", "source": contentFixtureSource("/missing", "missing", "p", 500)})
	ops = append(ops, map[string]any{"method": "setReadiness", "readiness": ContentReadinessWrite{State: "degraded", Discovered: 4, Indexed: 3, Degraded: 1}}, map[string]any{"method": "query", "query": "needle"}, map[string]any{"method": "suggest", "query": "needle"}, map[string]any{"method": "query", "query": ""})
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "content", "operations": ops})
	var observations []map[string]json.RawMessage
	if err := json.Unmarshal(source["observations"], &observations); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(context.Background(), DatabaseOptions{Path: filepath.Join(t.TempDir(), "content.sqlite"), Kind: Content})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	index, _ := NewContentIndex(db, "fixture", func() time.Time { return time.UnixMilli(42) })
	ctx := context.Background()
	for n, row := range ops {
		var result any = "$undefined"
		var err error
		data := raw(row)
		var r struct {
			Method, Path, Query, Value string
			Source                     ContentSource
			Documents                  []ContentDocument
			Readiness                  ContentReadinessWrite
			Options                    ContentQueryOptions
			Limit                      *float64
		}
		if err = json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		switch r.Method {
		case "replace", "append":
			err = index.ApplySource(ctx, r.Source, r.Method == "append", func(yield func(ContentDocument) error) error {
				for _, d := range r.Documents {
					d.NormalizedBody = NormalizeContent(d.Body)
					if e := yield(d); e != nil {
						return e
					}
				}
				return nil
			})
		case "delete":
			err = index.DeleteSource(ctx, r.Path)
		case "get":
			result, err = index.GetSource(ctx, r.Path)
		case "list":
			result, err = index.ListSources(ctx)
		case "count":
			result, err = index.CountSources(ctx)
		case "readiness":
			result, err = index.GetReadiness(ctx)
		case "setReadiness":
			err = index.SetReadiness(ctx, r.Readiness)
		case "query":
			result, err = index.Query(ctx, r.Query, r.Options)
		case "suggest":
			result, err = index.Suggest(ctx, r.Query, r.Limit)
		case "normalize":
			result = NormalizeContent(r.Value)
		default:
			t.Fatal(r.Method)
		}
		observed := map[string]any{"result": result}
		if err != nil {
			observed = map[string]any{"error": err.Error()}
		}
		if !reflect.DeepEqual(decoded(observed), decoded(observations[n])) {
			t.Fatalf("Source content row %d %s\nnative=%s\nsource=%s", n, data, raw(observed), raw(observations[n]))
		}
	}
}
func TestSourceFingerprintCapturesActualFileIdentityAndBytes(t *testing.T) {
	home := disposableHome(t)
	p := filepath.Join(home, "source.jsonl")
	_ = os.WriteFile(p, []byte("first\n中文 partial"), 0600)
	observed := sourceIndex(t, home, map[string]any{"action": "fingerprint", "operations": []any{map[string]any{"method": "capture", "indexedBytes": 6, "parserVersion": 1}}})
	var rows []struct {
		Fingerprint Fingerprint
		Serialized  string
	}
	_ = json.Unmarshal(observed["observations"], &rows)
	native, err := CaptureFingerprint(context.Background(), p, 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*native, rows[0].Fingerprint) || SerializeFingerprint(*native) != rows[0].Serialized {
		t.Fatalf("Actual Source fingerprint differs\nnative=%s\nSource=%s", raw(native), raw(rows[0]))
	}
	if !reflect.DeepEqual(DeserializeFingerprint(rows[0].Serialized), native) {
		t.Fatal("Stored Source fingerprint cannot deserialize")
	}
	cases := []struct {
		mutate func()
		parser int64
		want   SourceChange
	}{{func() {}, 1, SourceChange{Kind: "unchanged"}}, {func() {
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
		_, _ = f.WriteString(" completed\n")
		_ = f.Close()
	}, 1, SourceChange{Kind: "append", ReadFrom: 6}}, {func() {}, 2, SourceChange{Kind: "rebuild", Reason: "parser-version"}}, {func() { _ = os.Truncate(p, 2) }, 1, SourceChange{Kind: "rebuild", Reason: "truncate"}}, {func() { _ = os.Remove(p) }, 1, SourceChange{Kind: "deleted"}}}
	for n, c := range cases {
		c.mutate()
		s := sourceIndex(t, home, map[string]any{"action": "fingerprint", "previous": rows[0].Fingerprint, "operations": []any{map[string]any{"method": "detect", "parserVersion": c.parser}}})
		var got []SourceChange
		_ = json.Unmarshal(s["observations"], &got)
		actual := DetectSourceChange(context.Background(), p, *native, c.parser)
		if !reflect.DeepEqual(actual, c.want) || !reflect.DeepEqual(actual, got[0]) {
			t.Fatalf("Fingerprint transition %d native=%s Source=%s wanted=%s", n, raw(actual), raw(got), raw(c.want))
		}
	}
}
