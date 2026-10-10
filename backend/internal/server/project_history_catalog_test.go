package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
)

func TestProjectHistoryActualCanonicalCatalogMatchesSourceInAllIndexModes(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("%08d-bbbb-cccc-dddd-eeeeeeeeeeee", n) }
	file := func(project string, n int) string { return "$CONFIG/projects/" + project + "/" + id(n) + ".jsonl" }
	content := func(n int, root, work string, stamp string) string {
		rows := []map[string]any{{"type": "session-meta", "workDir": work, "permissionMode": "plan", "runtimeProviderId": "fixture", "runtimeModelId": "fixture-model"}}
		if root != work {
			rows = append(rows, map[string]any{"type": "worktree-state", "worktreeSession": map[string]any{"originalCwd": root, "worktreePath": work, "worktreeName": "old", "sessionId": id(n)}})
		}
		rows = append(rows, map[string]any{"type": "user", "uuid": fmt.Sprint("msg", n), "timestamp": stamp, "message": map[string]any{"role": "user", "content": fmt.Sprint("History ", n)}})
		out := ""
		for _, row := range rows {
			raw, _ := json.Marshal(row)
			out += string(raw) + "\n"
		}
		return out
	}
	ops := []map[string]any{
		{"method": "write", "path": file("root", 0), "write": content(0, "$WORKSPACE", "$WORKSPACE", "2020-01-01T00:00:20Z")},
		{"method": "write", "path": file("external", 1), "write": content(1, "$WORKSPACE", "$HOME/external-worktree", "2020-01-01T00:00:19Z"), "mkdir": "$HOME/external-worktree"},
		{"method": "write", "path": file("removed", 2), "write": content(2, "$WORKSPACE", "$WORKSPACE/.claude/worktrees/removed", "2020-01-01T00:00:18Z")},
		{"method": "write", "path": file("other", 3), "write": content(3, "$HOME/another-root", "$HOME/another-root", "2020-01-01T00:00:17Z")},
		{"method": "write", "path": file("duplicate", 0), "write": content(0, "$WORKSPACE", "$WORKSPACE", "2019-01-01T00:00:20Z")},
		{"method": "write", "path": file("worker", 9), "write": `{"type":"system","timestamp":"2026-10-06T00:00:00Z","entrypoint":"claude-desktop-team-worker","message":{"role":"system","content":"worker"}}` + "\n"},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2}, "save": "beforeIndex"},
		{"method": "start"},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2}, "save": "first"},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2, "cursor": "$beforeIndex"}},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$HOME/another-root"}},
		{"method": "notify", "path": file("removed", 2), "append": `{"type":"user","timestamp":"2030-01-01T00:00:00Z","message":{"role":"user","content":"Updated"}}` + "\n", "mutation": true},
		{"method": "notify", "path": file("root", 4), "write": content(4, "$WORKSPACE", "$WORKSPACE", "2031-01-01T00:00:00Z"), "mutation": true},
		{"method": "notify", "path": file("external", 1), "remove": true, "mutation": true},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2, "cursor": "$first"}},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2}, "save": "updated"},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "limit": 2, "cursor": "$updated"}},
		{"method": "projectHistory", "options": map[string]any{"projectRoot": "$WORKSPACE", "beforeModifiedAt": "2020-01-01T00:00:20Z", "beforeId": id(0)}},
	}
	for _, mode := range []string{"off", "shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			source := sessionConsumerSource(t, mode, ops)
			s, index, home, clock := consumerFixture(t, mode)
			expand := func(value string) string {
				return strings.NewReplacer("$HOME", home, "$CONFIG", s.options.ConfigDir, "$WORKSPACE", s.options.WorkDir).Replace(value)
			}
			h := s.projectHistoryManager()
			h.source.now = func() int64 { return clock.UnixMilli() }
			saved := map[string]string{}
			snapshots := map[string]int{}
			observed := 0
			ctx := context.Background()
			for _, op := range ops {
				*clock = clock.Add(time.Second)
				path, _ := op["path"].(string)
				path = expand(path)
				if dir, ok := op["mkdir"].(string); ok {
					_ = os.MkdirAll(expand(dir), 0700)
				}
				if data, ok := op["write"].(string); ok {
					_ = os.MkdirAll(filepath.Dir(path), 0700)
					_ = os.WriteFile(path, []byte(expand(data)), 0600)
					_ = os.Chtimes(path, time.Unix(1791244800, 0), time.Unix(1791244800, 0))
				}
				if data, ok := op["append"].(string); ok {
					f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if e != nil {
						t.Fatal(e)
					}
					_, _ = f.WriteString(expand(data))
					_ = f.Close()
					_ = os.Chtimes(path, time.Unix(1791331200, 0), time.Unix(1791331200, 0))
				}
				if op["remove"] == true {
					_ = os.Remove(path)
				}
				if op["mutation"] == true {
					s.noteSessionMutation(path)
				}
				switch op["method"] {
				case "start":
					if e := index.Start(ctx); e != nil {
						t.Fatal(e)
					}
					if e := index.WaitInitial(ctx); e != nil {
						t.Fatal(e)
					}
				case "notify":
					if mode != "off" {
						if e := index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{path}}); e != nil {
							t.Fatal(e)
						}
					}
				case "projectHistory":
					options := op["options"].(map[string]any)
					o := projectHistoryOptions{root: expand(options["projectRoot"].(string))}
					if n, ok := options["limit"].(int); ok {
						o.limit = n
						o.hasLimit = true
					}
					for key, target := range map[string]**string{"cursor": &o.cursor, "beforeId": &o.beforeID, "beforeModifiedAt": &o.beforeModifiedAt} {
						if v, ok := options[key].(string); ok {
							if key == "cursor" && strings.HasPrefix(v, "$") {
								v = saved[v[1:]]
							}
							*target = &v
						}
					}
					page, e := h.list(ctx, o)
					if observed == 0 && (e != nil || len(page.Sessions) != 2) {
						t.Fatalf("Initial real project page must contain two canonical sessions: %v %#v", e, page)
					}
					var result map[string]any
					if e != nil {
						api := e.(*apiError)
						result = map[string]any{"error": map[string]any{"status": api.status, "code": api.code, "message": api.message}}
					} else {
						var cursor any
						if page.NextCursor != nil {
							if name, ok := op["save"].(string); ok {
								saved[name] = *page.NextCursor
							}
							snapshot, offset, _ := decodeProjectHistoryCursor(*page.NextCursor)
							if snapshots[snapshot] == 0 {
								snapshots[snapshot] = len(snapshots) + 1
							}
							cursor = fmt.Sprintf("%d:%d", snapshots[snapshot], offset)
						}
						result = map[string]any{"result": map[string]any{"sessions": page.Sessions, "nextCursor": cursor}}
					}
					actual := plainConsumer(result, home)
					var want any
					_ = json.Unmarshal(source[observed], &want)
					if !reflect.DeepEqual(want, actual) {
						a, _ := json.Marshal(actual)
						t.Errorf("row%d Source%s Go%s", observed, source[observed], a)
					}
					observed++
				}
			}
			if observed != len(source) {
				t.Fatal(observed, len(source))
			}
		})
	}
}
