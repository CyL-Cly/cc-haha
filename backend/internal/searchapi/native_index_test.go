package searchapi

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func newIndexedSearchFixture(t *testing.T, files []map[string]any, rg, mode string) (*fixture, *localindex.Coordinator, *NativeIndexAdapter) {
	t.Helper()
	f := newSearchFixture(t, files, rg, false, false)
	parser := activitystatsapi.NewIndexDateParser(time.UTC)
	compare := collate.New(language.English)
	primary, e := localindex.NewCoordinator(localindex.CoordinatorOptions{Scope: f.config, Mode: mode, Budget: sessionstore.NewBudget(), Reduction: localindex.ReductionOptions{ParseDate: parser.Parse, Compare: compare.CompareString, Location: time.UTC, Platform: runtime.GOOS}, SourceMetadata: func(path string) (localindex.SourceMetadata, error) {
		info, e := os.Stat(path)
		if e != nil {
			return localindex.SourceMetadata{}, e
		}
		return localindex.SourceMetadata{BirthtimeISO: "1970-01-01T00:00:00.000Z", MtimeISO: info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")}, nil
	}, WatcherOptions: localindex.WatchOptions{Debounce: time.Hour, MaxWait: time.Hour, SafetySweep: time.Hour}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = primary.Stop(context.Background()) })
	if e = primary.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = primary.WaitInitial(context.Background()); e != nil {
		t.Fatal(e)
	}
	adapter, e := NewNativeIndexAdapter(primary, f.config, parser.Parse)
	if e != nil {
		t.Fatal(e)
	}
	f.s.o.Metadata = adapter.Metadata
	f.s.o.ReadEntriesAtLines = adapter.ReadEntriesAtLines
	f.s.o.GetCandidatesForFilters = adapter.GetCandidatesForFilters
	return f, primary, adapter
}
func metadataPairs(values map[string]Metadata) any {
	if values == nil {
		return nil
	}
	paths := []string{}
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := []any{}
	for _, path := range paths {
		m := values[path]
		out = append(out, []any{path, map[string]any{"title": m.Title, "modifiedAt": m.ModifiedAt, "workDir": m.WorkDir, "projectPath": m.ProjectPath}})
	}
	return out
}
func TestNativeSessionConsumersAndSearchAgainstActualSource(t *testing.T) {
	user := `{"type":"user","uuid":"u1","timestamp":"2026-10-06T00:00:00Z","cwd":"/real-workspace","message":{"role":"user","content":"Needle first task"}}` + "\n"
	assistant := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-06T00:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Needle completed"}]}}` + "\n"
	files := []map[string]any{{"path": "$CONFIG/projects/p/s1.jsonl", "content": user + assistant + `{"type":"custom-title","customTitle":"Actual custom title"}` + "\n"}, {"path": "$CONFIG/projects/p/colon:s2.jsonl", "content": user}, {"path": "$CONFIG/projects/other/s3.jsonl", "content": assistant}, {"path": "$CONFIG/projects/p/s1/subagents/agent-nested.jsonl", "content": assistant}}
	cases := []map[string]any{{"kind": "metadata", "paths": []string{"$CONFIG/projects/p/s1.jsonl"}}, {"kind": "atLines", "path": "$CONFIG/projects/p/s1.jsonl", "lines": []int64{1, 2, 3}}, {"kind": "atLines", "path": "$CONFIG/projects/p/s1.jsonl", "lines": []int64{4}}, {"kind": "filters", "options": map[string]any{"project": "p"}}, {"kind": "filters", "options": map[string]any{"project": "p", "modifiedAfter": "2026-10-06T00:00:00Z", "modifiedBefore": "2026-10-06T00:01:00Z"}}, {"kind": "filters", "options": map[string]any{"project": "p", "modifiedAfter": "invalid date"}}, {"kind": "filters", "options": map[string]any{"modifiedAfter": "2026-10-01"}}, {"kind": "sessions", "query": "needle"}, {"kind": "sessions", "query": "needle", "options": map[string]any{"project": "p"}}, {"kind": "sessions", "query": "Needle", "options": map[string]any{"caseSensitive": true, "project": "p", "matchesPerSession": 1}}, {"kind": "sessions", "query": "needle", "options": map[string]any{"project": "p", "modifiedAfter": "2026-10-06T00:00:00Z"}}, {"kind": "sessions", "query": "needle", "path": "$CONFIG/projects/p/new.jsonl", "write": user, "options": map[string]any{"project": "p"}}, {"kind": "filters", "options": map[string]any{"project": "p"}}, {"kind": "sessions", "query": "needle", "path": "$CONFIG/projects/p/s1.jsonl", "write": assistant, "options": map[string]any{"project": "p"}}, {"kind": "atLines", "path": "$CONFIG/projects/p/s1.jsonl", "lines": []int64{1}}, {"kind": "sessions", "query": "needle", "path": "$CONFIG/projects/p/s1.jsonl", "reconcile": true, "options": map[string]any{"project": "p"}}, {"kind": "atLines", "path": "$CONFIG/projects/p/s1.jsonl", "lines": []int64{1}}, {"kind": "sessions", "query": "needle", "path": "$CONFIG/projects/p/new.jsonl", "remove": true, "options": map[string]any{"project": "p"}}}
	rg, _ := exec.LookPath("rg")
	for _, transport := range []string{"", rg} {
		if transport == "" && rg == "" { /* portable remains required */
		}
		for _, mode := range []string{"on", "shadow", "off"} {
			t.Run(mode+filepath.Base(transport), func(t *testing.T) {
				rows := sourceSearch(t, map[string]any{"primary": true, "mode": mode, "rg": transport, "files": files, "cases": cases})
				f, primary, adapter := newIndexedSearchFixture(t, files, transport, mode)
				ctx := context.Background()
				for n, row := range cases {
					path, _ := row["path"].(string)
					path = f.expand(path)
					if text, ok := row["write"].(string); ok {
						_ = os.MkdirAll(filepath.Dir(path), 0700)
						_ = os.WriteFile(path, []byte(text), 0600)
						_ = os.Chtimes(path, time.Unix(1791331200, 0), time.Unix(1791331200, 0))
					}
					if row["remove"] == true {
						_ = os.RemoveAll(path)
					}
					if row["reconcile"] == true && mode != "off" {
						if e := primary.Reconcile(ctx, localindex.ReconciliationBatch{FullSweep: true}); e != nil {
							t.Fatal(e)
						}
					}
					var observed any
					var e error
					switch row["kind"] {
					case "metadata":
						paths := []string{}
						for _, p := range row["paths"].([]string) {
							paths = append(paths, f.expand(p))
						}
						values, err := adapter.Metadata(ctx, paths)
						e = err
						observed = metadataPairs(values)
					case "atLines":
						lines := map[int64]bool{}
						for _, line := range row["lines"].([]int64) {
							lines[line] = true
						}
						observed, e = adapter.ReadEntriesAtLines(ctx, path, lines)
					case "filters":
						var options protocol.JSONObject
						_ = json.Unmarshal(raw(row["options"]), &options)
						o := localindex.ContentQueryOptions{Project: optionalString(options.Get("project"))}
						for _, field := range []string{"modifiedAfter", "modifiedBefore"} {
							if truthy(options.Get(field)) {
								ms, valid, err := adapter.parseDate(str(options.Get(field)))
								if err != nil {
									t.Fatal(err)
								}
								if !valid {
									ms = math.NaN()
								}
								if field == "modifiedAfter" {
									o.ModifiedAfterMs = &ms
								} else {
									o.ModifiedBeforeMs = &ms
								}
							}
						}
						values, err := adapter.GetCandidatesForFilters(ctx, o)
						e = err
						if values != nil {
							pairs := []any{}
							for _, candidate := range values {
								m := candidate.Metadata
								pairs = append(pairs, []any{candidate.Path, map[string]any{"title": m.Title, "modifiedAt": m.ModifiedAt, "workDir": m.WorkDir, "projectPath": m.ProjectPath}})
							}
							observed = pairs
						}
					default:
						observed, e = observeService(f, row)
					}
					if e != nil {
						t.Fatal(e)
					}
					actual := map[string]any{"result": f.reduce(observed)}
					if !reflect.DeepEqual(decoded(actual), decoded(rows[n])) {
						t.Fatalf("Source native search %d %s\nNative=%s\nSource=%s", n, raw(row), raw(actual), rows[n])
					}
				}
			})
		}
	}
}
