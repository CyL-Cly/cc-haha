package trajectory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func writeFixture(t *testing.T, rows []Object) (string, []Entry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := []byte{}
	entries := []Entry{}
	for _, row := range rows {
		b := raw(row)
		start := int64(len(body))
		body = append(body, b...)
		entries = append(entries, Entry{row, start, int64(len(body)), 0})
		body = append(body, '\n')
	}
	if e := os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	return path, entries
}
func prompt(id, content string) Object {
	return Object{"type": "user", "uuid": id, "timestamp": "2026-10-12T00:00:01.000Z", "message": Object{"role": "user", "content": content}}
}
func TestTrajectoryPagesAppendPartialTailRewriteAndRowIdentity(t *testing.T) {
	rows := []Object{}
	for n := 0; n < 1205; n++ {
		rows = append(rows, prompt(fmt.Sprintf("u%d", n), fmt.Sprintf("prompt%d", n)))
	}
	path, entries := writeFixture(t, rows)
	s := New(sessionstore.NewReader(nil))
	source := Source{Path: path, SnapshotPath: filepath.Join(filepath.Dir(path), "prompt-snapshots.jsonl"), Scope: "main", Main: true}
	page, e := s.Page(context.Background(), source, PageOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Rows) != 1000 || page.TurnBase == nil || *page.TurnBase != 205 || page.OlderCursor == nil || page.HistoryComplete || page.Rows[0]["turn"] != 206 {
		t.Fatalf("wrong tail/turn base: %d %+v", len(page.Rows), page)
	}
	older, e := s.Page(context.Background(), source, PageOptions{Cursor: *page.OlderCursor})
	if e != nil || len(older.Rows) != 205 || older.TailToken != "" || older.Rows[0]["turn"] != 1 {
		t.Fatalf("older: %d %v", len(older.Rows), e)
	}
	last := entries[len(entries)-1]
	loc := fmt.Sprintf("%d-%d", last.Start, last.End)
	detail, e := s.Detail(context.Background(), source, "u:u1204", loc)
	if e != nil || len(detail.Entries) != 1 || detail.Entries[0]["uuid"] != "u1204" {
		t.Fatalf("detail: %+v %v", detail, e)
	}
	if _, e = s.Detail(context.Background(), source, "u:wrong", loc); e == nil {
		t.Fatal("locator returned a different row")
	}
	if _, e = s.Detail(context.Background(), source, "u:u1204", fmt.Sprintf("%d-%d", last.Start+1, last.End)); e == nil {
		t.Fatal("partial line accepted")
	}
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b := raw(prompt("new", "next"))
	if _, e = f.Write(b); e != nil {
		t.Fatal(e)
	}
	app, e := s.Page(context.Background(), source, PageOptions{After: page.TailToken})
	if e != nil || len(app.Rows) != 0 || app.TailToken != page.TailToken {
		t.Fatalf("incomplete line consumed: %d %v", len(app.Rows), e)
	}
	f.WriteString("\n")
	app, e = s.Page(context.Background(), source, PageOptions{After: page.TailToken})
	if e != nil || len(app.Rows) != 1 || app.Rows[0]["id"] != "u:new" || app.Rows[0]["turn"] != 1206 {
		t.Fatalf("append: %+v %v", app, e)
	}
	data, _ := os.ReadFile(path)
	data[2] = 'X'
	os.WriteFile(path, data, 0600)
	if _, e = s.Page(context.Background(), source, PageOptions{After: page.TailToken}); e == nil {
		t.Fatal("rewrite accepted by old token")
	}
	if _, e = s.Page(context.Background(), source, PageOptions{Cursor: *page.OlderCursor}); e == nil {
		t.Fatal("rewrite accepted by old history cursor")
	}
}
func TestTrajectorySnapshotScopeBlobAndSideQueryFiltering(t *testing.T) {
	path, _ := writeFixture(t, []Object{prompt("u", "go")})
	side := filepath.Join(filepath.Dir(path), "snapshots.jsonl")
	rows := []Object{{"t": "blob", "k": "sys", "h": "1111111111111111", "text": "original system"}, {"t": "req", "scope": "main", "sys": "1111111111111111", "tools": "2222222222222222", "ts": "a", "qs": "sdk", "model": "fixture", "sysChars": 1500, "toolCount": 2}, {"t": "req", "scope": "main", "sys": "aaaaaaaaaaaaaaaa", "tools": "bbbbbbbbbbbbbbbb", "ts": "b", "qs": "compact"}, {"t": "req", "scope": "agent-child", "sys": "1111111111111111", "tools": "2222222222222222", "ts": "c", "qs": "agent:builtin:Explore"}, {"t": "ctx", "scope": "main", "ctx": "cccccccccccccccc", "ts": "d"}, {"t": "req", "scope": "main", "sys": "dddddddddddddddd", "tools": "2222222222222222", "ts": "e", "qs": "sdk"}}
	b := []byte{}
	for _, r := range rows {
		b = append(b, raw(r)...)
		b = append(b, '\n')
	}
	os.WriteFile(side, b, 0600)
	s := New(sessionstore.NewReader(nil))
	main, e := s.Page(context.Background(), Source{path, side, "main", true}, PageOptions{})
	if e != nil {
		t.Fatal(e)
	}
	snap := *main.Snapshots
	if len(snap) != 3 || snap[0]["preview"] != "system prompt (1,500 chars, 2 tools)" || snap[2]["label"] != "system" || object(snap[2]["snapshot"])["prevSys"] != "1111111111111111" {
		t.Fatalf("snapshot: %+v", snap)
	}
	child, e := s.Page(context.Background(), Source{path, side, "child", false}, PageOptions{})
	if e != nil || len(*child.Snapshots) != 1 {
		t.Fatalf("child scope: %+v %v", child, e)
	}
	blob, e := s.Snapshot(context.Background(), side, "1111111111111111")
	if e != nil || blob["text"] != "original system" {
		t.Fatalf("blob: %v %v", blob, e)
	}
	if _, e = s.Snapshot(context.Background(), side, "../secret"); e == nil {
		t.Fatal("invalid hash accepted")
	}
}
func TestTrajectoryOversizedToolResultIsRepresentedWithoutWholeBody(t *testing.T) {
	path, _ := writeFixture(t, []Object{prompt("u", "go"), {"type": "assistant", "uuid": "a", "timestamp": "2026-10-12T00:00:02Z", "message": Object{"id": "a", "content": []any{Object{"type": "tool_use", "id": "call", "name": "computer", "input": Object{"action": "screenshot"}}}}}})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"type":"user","uuid":"r","timestamp":"2026-10-12T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"` + strings.Repeat("x", sessionstore.HistorySemanticRecordBytes+1) + `"}]}}` + "\n")
	f.Close()
	s := New(sessionstore.NewReader(nil))
	source := Source{path, path + ".snap", "main", true}
	page, e := s.Page(context.Background(), source, PageOptions{})
	if e != nil {
		t.Fatal(e)
	}
	var tool Row
	for _, r := range page.Rows {
		if r["id"] == "t:call" {
			tool = r
		}
	}
	if tool == nil || tool["resultOmittedBytes"] == nil || page.Omitted != 0 {
		t.Fatalf("omitted tool not represented: %+v %d", tool, page.Omitted)
	}
	locs := tool["loc"].([][2]int64)
	parts := []string{}
	for _, r := range locs {
		parts = append(parts, fmt.Sprintf("%d-%d", r[0], r[1]))
	}
	detail, e := s.Detail(context.Background(), source, "t:call", strings.Join(parts, ","))
	if e != nil || !detail.Truncated || len(detail.Entries) != 1 {
		t.Fatalf("bounded oversized detail: %+v %v", detail, e)
	}
}
func TestTrajectoryCancellationMissingAndBudgets(t *testing.T) {
	s := New(sessionstore.NewReader(nil))
	missing := Source{filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "missing-snapshot"), "main", true}
	page, e := s.Page(context.Background(), missing, PageOptions{})
	if e != nil || !page.Missing || !page.HistoryComplete || page.Snapshots == nil {
		t.Fatal(page, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Page(ctx, missing, PageOptions{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = s.Page(context.Background(), missing, PageOptions{Cursor: "x", After: "y"}); e == nil {
		t.Fatal("cursor+after accepted")
	}
	for _, loc := range []string{"", "-1-4", "4-4", "0-4,4-9007199254740992"} {
		if _, e := ParseLoc(loc); e == nil {
			t.Fatal(loc)
		}
	}
	for _, agent := range []string{"../escape", "bad.name", strings.Repeat("x", 129)} {
		if _, e := AgentID(agent); e == nil {
			t.Fatal(agent)
		}
	}
}
