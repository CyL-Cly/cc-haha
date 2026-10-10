package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendRows(t *testing.T, path string, rows ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(strings.Join(rows, "\n") + "\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
}
func TestSidebarPreviewsUseActualSourceSummaryAndCanonicalProjectGroups(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	repo := filepath.Join(options.WorkDir, "repo")
	for _, path := range []string{filepath.Join(repo, ".git"), filepath.Join(repo, "src"), filepath.Join(options.WorkDir, "other")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	first := createTestSession(t, host, repo)
	second := createTestSession(t, host, filepath.Join(repo, "src"))
	third := createTestSession(t, host, filepath.Join(options.WorkDir, "other"))
	worker := createTestSession(t, host, repo)
	info, err := server.findSession(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path, `{"type":"user","timestamp":"2026-10-05T01:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"<note>hidden</note>first readable"}]}}`, `{"type":"ai-title","aiTitle":"AI fallback"}`, `{"type":"system","subtype":"local_command","content":"<command-name>/goal</command-name><command-args>Ship fixture</command-args>"}`)
	info, err = server.findSession(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path, `{"type":"user","timestamp":"2026-10-05T02:00:00.000Z","message":{"role":"user","content":"newer fixture"}}`)
	info, err = server.findSession(context.Background(), third)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path, `{"type":"user","timestamp":"2026-10-05T03:00:00.000Z","message":{"role":"user","content":"other fixture"}}`)
	info, err = server.findSession(context.Background(), worker)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path, `{"type":"system","entrypoint":"claude-desktop-team-worker","message":{"role":"system","content":"worker fixture"}}`)
	raw := requestJSON(t, host, "GET", "/api/sessions?view=sidebar&perProjectLimit=1", "", 200)
	var result struct {
		Sessions []sessionInfo
		Projects []struct {
			ProjectRoot string
			Total       int
		}
		Total int
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 || len(result.Sessions) != 2 || len(result.Projects) != 2 || result.Sessions[0].ID != third || result.Sessions[1].ID != second {
		t.Fatalf("sidebar grouping/order %s", raw)
	}
	found := false
	for _, project := range result.Projects {
		if project.ProjectRoot == canonicalPath(repo) && project.Total == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("nested working directory did not join main repo group")
	}
	raw = requestJSON(t, host, "GET", "/api/sessions/"+first+"/summary", "", 200)
	var summary sessionInfo
	_ = json.Unmarshal(raw, &summary)
	if summary.Title != "/goal Ship fixture" {
		t.Fatalf("Source title reducer changed: %q", summary.Title)
	}
	for _, query := range []string{"0", "-1", "1.5", "9007199254740992", ""} {
		requestJSON(t, host, "GET", "/api/sessions?view=sidebar&perProjectLimit="+query, "", 400)
	}
}
func TestCanonicalProjectWorktreeRequiresActualValidatedBacklink(t *testing.T) {
	home := t.TempDir()
	repo, worktree := filepath.Join(home, "main"), filepath.Join(home, "checkout")
	directory := filepath.Join(repo, ".git", "worktrees", "fixture")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{filepath.Join(worktree, ".git"): "gitdir: " + directory + "\n", filepath.Join(directory, "commondir"): "../..\n", filepath.Join(directory, "gitdir"): filepath.Join(worktree, ".git") + "\n"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if root := canonicalGitRoot(worktree); root != repo {
		t.Fatalf("legitimate linked worktree root=%q", root)
	}
	if err := os.WriteFile(filepath.Join(directory, "gitdir"), []byte(filepath.Join(repo, ".git")), 0600); err != nil {
		t.Fatal(err)
	}
	if root := canonicalGitRoot(worktree); root != worktree {
		t.Fatal("invalid backlink borrowed main repo identity")
	}
}
func TestFullHistoryModeUsesReaderBudgetAndRejectsCursorCombination(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	rows := []string{}
	for n := 0; n < 600; n++ {
		row, _ := json.Marshal(map[string]any{"type": "user", "uuid": strings.Repeat("x", n%3+1), "message": map[string]string{"role": "user", "content": "fixture"}})
		rows = append(rows, string(row))
	}
	appendRows(t, info.Path, rows...)
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages?mode=full", "", 200)
	var result struct{ Messages []json.RawMessage }
	_ = json.Unmarshal(raw, &result)
	if len(result.Messages) != 600 {
		t.Fatalf("full history silently used page mode: %d", len(result.Messages))
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages?mode=invalid", "", 400)
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages?mode=full&cursor=invalid", "", 400)
}
