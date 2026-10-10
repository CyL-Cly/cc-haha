package localindex

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sqliteString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func TestSessionIndexActualSourcePublicRowsAndMetadata(t *testing.T) {
	seeds := []string{}
	titles := []string{"exact needle", "needle prefix", "any needle middle", "workspaceonly", "中文标题", "ΟΣ", "ẞ sharp", "İ K", "independent worker"}
	for n, title := range titles {
		path := fmt.Sprintf("/p/%d.jsonl", n)
		id := fmt.Sprintf("session-%d", n)
		worker := 0
		if n == 8 {
			worker = 1
		}
		project := []string{"p", "q"}[n%2]
		seeds = append(seeds, fmt.Sprintf("INSERT INTO source_files VALUES(%s,'transcript',100,%.0f,NULL,'fingerprint',90,9,'ready',NULL,42)", sqliteString(path), float64(1000+n)))
		repository := "NULL"
		worktree := "NULL"
		provider := 0
		if n == 0 {
			repository = sqliteString(`{"repoRoot":"/repo"}`)
			worktree = sqliteString("null")
			provider = 1
		}
		if n == 1 {
			repository = sqliteString("bad-json")
			worktree = sqliteString("bad-json")
		}
		seeds = append(seeds, fmt.Sprintf("INSERT INTO sessions(transcript_path,session_id,project_path,title,created_at,modified_at,modified_at_ms,message_count,work_dir,permission_mode,runtime_provider_id,runtime_provider_present,runtime_model_id,effort_level,repository_json,worktree_session_json,is_team_worker) VALUES(%s,%s,%s,%s,'2026-10-01T00:00:00Z','2026-10-06T00:00:00Z',%d,3,'/needle/workspace','plan',NULL,%d,'deepseek-flash','high',%s,%s,%d)", sqliteString(path), sqliteString(id), sqliteString(project), sqliteString(title), 1000+n, provider, repository, worktree, worker))
		seeds = append(seeds, fmt.Sprintf("INSERT INTO session_entries VALUES(%s,0,1,0,40,'user','id','user','2026-10-06T00:00:00Z',NULL); INSERT INTO session_entries VALUES(%s,1,3,40,50,'assistant',NULL,'assistant',NULL,'parent')", sqliteString(path), sqliteString(path)))
	}
	seeds = append(seeds, "INSERT INTO backfill_state VALUES('fixture','ready','/p/8.jsonl',9,9,0,NULL,42)")
	ops := []map[string]any{{"method": "list"}, {"method": "list", "options": map[string]any{"project": "p", "limit": 2, "offset": 1}}, {"method": "list", "options": map[string]any{"limit": 0, "offset": -1}}, {"method": "get", "id": "session-8"}, {"method": "get", "id": "missing"}, {"method": "suggest", "ids": []string{"session-8", "session-8", "session-0", "missing"}}, {"method": "suggest", "ids": []string{}}, {"method": "find", "id": "session-1"}, {"method": "candidates", "options": map[string]any{"project": "p", "modifiedAfterMs": 1002, "modifiedBeforeMs": 1006}}, {"method": "sources"}, {"method": "source", "path": "/p/0.jsonl"}, {"method": "source", "path": "missing"}, {"method": "count"}, {"method": "backfill", "scope": "fixture"}, {"method": "backfill", "scope": "missing"}, {"method": "locators", "path": "/p/0.jsonl"}, {"method": "locators", "path": "/p/0.jsonl", "types": []string{}}, {"method": "locators", "path": "/p/0.jsonl", "types": []string{"assistant", "assistant", ""}}, {"method": "locators", "path": "missing"}, {"method": "projection", "path": "/p/0.jsonl"}, {"method": "projection", "path": "/p/1.jsonl"}}
	for _, q := range []string{"needle", "exact needle", "session-1", "workspaceonly", "中文", "ΟΣ", "ος", "ß", "ẞ", "i", "k", "İ", "no match", ""} {
		ops = append(ops, map[string]any{"method": "search", "query": q}, map[string]any{"method": "search", "query": q, "options": map[string]any{"limit": 2, "offset": 1}})
	}
	ops = append(ops, map[string]any{"method": "updateTitle", "id": "session-0", "title": "changed"}, map[string]any{"method": "get", "id": "session-0"}, map[string]any{"method": "updateTitle", "id": "missing", "title": "changed"})
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "sessionIndex", "seed": seeds, "operations": ops})
	var rows []json.RawMessage
	_ = json.Unmarshal(source["observations"], &rows)
	ctx := context.Background()
	db, e := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(t.TempDir(), "sessions.sqlite"), Kind: Primary})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.Transaction(ctx, func(op *Operation) error {
		for _, sql := range seeds {
			if e := op.Exec(sql); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	index, _ := NewSessionIndex(db)
	for n, row := range ops {
		var input struct {
			Method, ID, Path, Scope, Query, Title string
			IDs                                   []string
			Types                                 *[]string
			Options                               struct {
				Project                                          *string
				Limit, Offset, ModifiedAfterMs, ModifiedBeforeMs *float64
			}
		}
		_ = json.Unmarshal(raw(row), &input)
		var result any
		switch input.Method {
		case "list":
			result, e = index.List(ctx, SessionListOptions{input.Options.Project, input.Options.Limit, input.Options.Offset})
		case "get":
			result, e = index.Get(ctx, input.ID)
		case "suggest":
			result, e = index.SuggestionMetadata(ctx, input.IDs)
		case "find":
			result, e = index.FindFiles(ctx, input.ID)
		case "candidates":
			result, e = index.FindCandidates(ctx, ContentQueryOptions{Project: input.Options.Project, ModifiedAfterMs: input.Options.ModifiedAfterMs, ModifiedBeforeMs: input.Options.ModifiedBeforeMs})
		case "sources":
			result, e = index.ListSources(ctx)
		case "source":
			result, e = index.GetSource(ctx, input.Path)
		case "count":
			result, e = index.CountSources(ctx)
		case "backfill":
			result, e = index.GetBackfill(ctx, input.Scope)
		case "locators":
			result, e = index.Locators(ctx, input.Path, input.Types)
		case "projection":
			result, e = index.ProjectionSeed(ctx, input.Path)
		case "search":
			result, e = index.SearchMetadata(ctx, input.Query, input.Options.Limit, input.Options.Offset)
		case "updateTitle":
			result, e = index.UpdateTitle(ctx, input.ID, input.Title)
		default:
			t.Fatal(input.Method)
		}
		observation := map[string]any{"result": result}
		if e != nil {
			observation = map[string]any{"error": e.Error()}
		}
		if !reflect.DeepEqual(decoded(observation), decoded(rows[n])) {
			t.Fatalf("Session index row %d %s native=%s Source=%s", n, raw(row), raw(observation), rows[n])
		}
	}
}
