package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func historyTestRow(n int, root string) projectHistoryRow {
	id := fmt.Sprintf("%08d-bbbb-cccc-dddd-eeeeeeeeeeee", n)
	return projectHistoryRow{root: root, info: sessionInfo{ID: id, Title: fmt.Sprintf("History %d", n), CreatedAt: "2020-01-01T00:00:00Z", ModifiedAt: fmt.Sprintf("2020-01-01T00:00:%02dZ", 20-n), MessageCount: 1, ProjectPath: "p", WorkDir: root, ProjectRoot: &root, WorkspaceState: "available", WorkDirExists: true, Path: "/private/p/" + id + ".jsonl"}}
}
func historyFixtureRows(rows []projectHistoryRow) []map[string]any {
	out := []map[string]any{}
	for _, row := range rows {
		out = append(out, map[string]any{"info": row.info, "path": row.info.Path, "root": row.root})
	}
	return out
}
func TestProjectHistorySnapshotPagingMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	rows := []projectHistoryRow{}
	for n := 0; n < 8; n++ {
		rows = append(rows, historyTestRow(n, "/project"))
	}
	rows = append(rows, historyTestRow(8, "/other"))
	duplicate := rows[1]
	duplicate.info.ModifiedAt = "2019-01-01T00:00:00Z"
	duplicate.info.Path = "/private/duplicate/" + duplicate.info.ID + ".jsonl"
	rows = append(rows, duplicate)
	list := func(root string, limit int, save, cursor string) map[string]any {
		opts := map[string]any{"projectRoot": root, "limit": limit}
		if cursor != "" {
			opts["cursor"] = cursor
		}
		op := map[string]any{"kind": "list", "options": opts}
		if save != "" {
			op["save"] = save
		}
		return op
	}
	ops := []map[string]any{
		list("/project", 1, "first", ""), list("/project", 2, "", "$first"),
		{"kind": "delete", "ids": []string{rows[1].info.ID, rows[2].info.ID, rows[3].info.ID, rows[4].info.ID, rows[5].info.ID}},
		list("/project", 2, "sparse", "$first"), list("/project", 2, "", "$sparse"), list("/other", 1, "", "$first"),
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "cursor": "$first"}, "offset": 99},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "cursor": "$first"}, "offset": -1},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "cursor": "$first"}, "offset": 1.5},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "cursor": "not-a-cursor"}},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "cursor": ""}},
		list("", 1, "", ""), list(strings.Repeat("界", 8193), 1, "", ""), list("a\x00b", 1, "", ""), list("/project", 0, "", ""), list("/empty", 1, "", ""),
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "beforeId": rows[0].info.ID}},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "beforeModifiedAt": "nonsense", "beforeId": rows[0].info.ID}},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "beforeModifiedAt": "2020-01-01T00:00:20Z", "beforeId": rows[0].info.ID, "cursor": "$first"}},
		{"kind": "list", "options": map[string]any{"projectRoot": "/project", "beforeModifiedAt": "2020-01-01T00:00:20Z", "beforeId": rows[0].info.ID}},
		{"kind": "advance", "ms": 300001}, list("/project", 1, "", "$first"), list("/project", 1, "evicted", ""),
	}
	for n := 0; n < 17; n++ {
		ops = append(ops, list("/project", 1, "newest", ""))
	}
	ops = append(ops, list("/project", 1, "", "$evicted"), list("/project", 1, "", "$newest"))
	updated := append([]projectHistoryRow{}, rows...)
	updated[0].info.Title = "Current first title"
	updated[6].info.ModifiedAt = "2030-01-01T00:00:00Z"
	ops = append(ops, map[string]any{"kind": "rows", "rows": historyFixtureRows(updated)}, list("/project", 2, "newrows", ""), list("/project", 1, "", "$newest"), map[string]any{"kind": "mutate", "hydrates": 2}, list("/project", 1, "", ""), map[string]any{"kind": "mutate", "hydrates": 3}, list("/project", 1, "", ""), map[string]any{"kind": "revision"}, map[string]any{"kind": "mutate", "loads": 2}, list("/project", 1, "", ""), map[string]any{"kind": "revision"}, map[string]any{"kind": "mutate", "loads": 3}, list("/project", 1, "", ""), map[string]any{"kind": "scope", "scope": "changed"}, list("/project", 1, "", "$newrows"))
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-project-history.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PROJECT_HISTORY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"home": home, "rows": historyFixtureRows(rows), "operations": ops})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source []any
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	now, revision, mutation := int64(0), 0, 0
	scope := "private-scope"
	loads, hydrates, mutateHydrates, mutateLoads := 0, 0, 0, 0
	deleted := map[string]bool{}
	h := newProjectSessionHistory(projectHistorySource{now: func() int64 { return now }, scope: func() string { return scope }, revision: func() string { return fmt.Sprint(revision) }, mutation: func() string { return fmt.Sprint(mutation) }, load: func(context.Context) ([]projectHistoryRow, error) {
		loads++
		if mutateLoads > 0 {
			mutateLoads--
			revision++
		}
		return rows, nil
	}, hydrate: func(_ context.Context, row projectHistoryRow) (*sessionInfo, error) {
		hydrates++
		if mutateHydrates > 0 {
			mutateHydrates--
			mutation++
		}
		if deleted[row.info.ID] {
			return nil, nil
		}
		info := row.info
		return &info, nil
	}})
	saved := map[string]string{}
	ids := map[string]int{}
	observed := 0
	for _, op := range ops {
		switch op["kind"] {
		case "advance":
			now += int64(op["ms"].(int))
			continue
		case "delete":
			for _, id := range op["ids"].([]string) {
				deleted[id] = true
			}
			continue
		case "revision":
			revision++
			continue
		case "scope":
			scope = op["scope"].(string)
			continue
		case "mutate":
			if n, ok := op["hydrates"].(int); ok {
				mutateHydrates = n
			} else {
				mutateHydrates = 0
			}
			if n, ok := op["loads"].(int); ok {
				mutateLoads = n
			} else {
				mutateLoads = 0
			}
			continue
		case "rows":
			rows = updated
			revision++
			continue
		}
		options := op["options"].(map[string]any)
		o := projectHistoryOptions{root: options["projectRoot"].(string)}
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
		if offset, ok := op["offset"]; ok {
			raw, _ := base64.RawURLEncoding.DecodeString(*o.cursor)
			var object map[string]any
			_ = json.Unmarshal(raw, &object)
			object["offset"] = offset
			raw, _ = json.Marshal(object)
			v := base64.RawURLEncoding.EncodeToString(raw)
			o.cursor = &v
		}
		page, e := h.list(ctx, o)
		observation := map[string]any{"loads": loads, "hydrates": hydrates}
		if e != nil {
			api := e.(*apiError)
			observation["error"] = map[string]any{"status": api.status, "code": api.code, "message": api.message}
		} else {
			var cursor any
			if page.NextCursor != nil {
				if name, ok := op["save"].(string); ok {
					saved[name] = *page.NextCursor
				}
				id, offset, _ := decodeProjectHistoryCursor(*page.NextCursor)
				if ids[id] == 0 {
					ids[id] = len(ids) + 1
				}
				cursor = fmt.Sprintf("%d:%d", ids[id], offset)
			}
			observation["value"] = map[string]any{"sessions": page.Sessions, "nextCursor": cursor}
		}
		data, _ := json.Marshal(observation)
		var actual any
		_ = json.Unmarshal(data, &actual)
		if !reflect.DeepEqual(source[observed], actual) {
			want, _ := json.Marshal(source[observed])
			t.Errorf("row %d options %v Source %s Go %s", observed, options, want, data)
		}
		observed++
	}
	if observed != len(source) {
		t.Fatal(observed, len(source))
	}
}
