package server

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceStatusMergesActualTranscriptAndLatestNativeSnapshotWithoutFailedTools(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("workspace API opened Agent")
		return nil, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tool := func(id, name string, input any) string {
		return subagentRow(t, "message-"+id, "assistant", "2026-10-06T00:00:00.000Z", []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}, nil)
	}
	appendRows(t, info.Path,
		subagentRow(t, "root-user", "user", "2026-10-06T00:00:00.000Z", "change real file", nil),
		tool("write", "Write", map[string]any{"file_path": "actual.txt", "content": "a\nb\n"}),
		tool("edit", "Edit", map[string]any{"file_path": "actual.txt", "old_string": "b", "new_string": "c"}),
		tool("denied", "Write", map[string]any{"file_path": "denied.txt", "content": "no"}),
		subagentRow(t, "denied-result", "user", "2026-10-06T00:00:01.000Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "denied", "content": "denied", "is_error": true}}, nil))
	writeSearchFixture(t, options.WorkDir, "actual.txt", "a\nc\n")
	base := "/api/sessions/" + id + "/workspace/"
	status := requestJSON(t, host, "GET", base+"status", "", 200)
	var result struct {
		State        string
		IsGitRepo    bool
		ChangedFiles []workspaceChange
	}
	if err := json.Unmarshal(status, &result); err != nil || result.State != "ok" || result.IsGitRepo || len(result.ChangedFiles) != 1 || result.ChangedFiles[0].Path != "actual.txt" || result.ChangedFiles[0].Status != "added" || result.ChangedFiles[0].Additions != 3 || result.ChangedFiles[0].Deletions != 1 {
		t.Fatal(string(status), err)
	}
	diff := requestJSON(t, host, "GET", base+"diff?path=actual.txt", "", 200)
	if !strings.Contains(stringField(historyRecord(diff), "diff"), "+c") || !strings.Contains(stringField(historyRecord(diff), "diff"), "+a") {
		t.Fatal(string(diff))
	}
	backupDir := filepath.Join(options.ConfigDir, "file-history", id)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeSearchFixture(t, backupDir, "before", "a\nb\n")
	before, _ := json.Marshal(map[string]any{"type": "file-history-snapshot", "snapshot": map[string]any{"messageId": "root-user", "trackedFileBackups": map[string]any{"actual.txt": map[string]any{"backupFileName": nil}}}})
	updated, _ := json.Marshal(map[string]any{"type": "file-history-snapshot", "snapshot": map[string]any{"messageId": "root-user", "trackedFileBackups": map[string]any{"actual.txt": map[string]any{"backupFileName": "before"}}}})
	appendRows(t, info.Path, string(before), string(updated))
	status = requestJSON(t, host, "GET", base+"status", "", 200)
	_ = json.Unmarshal(status, &result)
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0].Status != "modified" || result.ChangedFiles[0].Additions != 1 || result.ChangedFiles[0].Deletions != 1 {
		t.Fatal("latest snapshot and net delta did not supersede transcript totals", string(status))
	}
	// Diff prefers the actual session tool changes, whereas status uses net snapshots.
	diff = requestJSON(t, host, "GET", base+"diff?path=actual.txt", "", 200)
	if strings.Count(stringField(historyRecord(diff), "diff"), "diff --session") != 2 {
		t.Fatal(string(diff))
	}
	requestJSON(t, host, "GET", base+"diff", "", 400)
	outside := requestJSON(t, host, "GET", base+"diff?path=../outside.txt", "", 200)
	if stringField(historyRecord(outside), "state") != "error" || stringField(historyRecord(outside), "error") != "Path is outside workspace: ../outside.txt" {
		t.Fatal(string(outside))
	}
	appendRows(t, info.Path, `{"broken":`)
	requestJSON(t, host, "GET", base+"status", "", 413)
}

func TestWorkspaceNativeGitStatusDiffAndSubtreeScope(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("workspace API opened Agent")
		return nil, nil
	})
	options.Environment = func() map[string]string {
		return map[string]string{"PATH": os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(options.HomeDir, "no-global"), "GIT_OPTIONAL_LOCKS": "0", "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_AUTHOR_DATE": "2026-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE": "2026-01-01T00:00:00+00:00"}
	}
	server, host := testHost(t, options)
	git := func(args ...string) {
		t.Helper()
		result := server.repositoryGit(context.Background(), options.WorkDir, args...)
		if result.code != 0 {
			t.Fatal(args, result)
		}
	}
	// Git history belongs only to this disposable, private fixture checkout.
	git("init", "-q", "-b", "fixture")
	writeSearchFixture(t, options.WorkDir, "sub/modified.txt", "before\n")
	writeSearchFixture(t, options.WorkDir, "outside.txt", "outside before\n")
	git("add", "--", "sub/modified.txt", "outside.txt")
	git("commit", "-q", "-m", "private fixture baseline")
	writeSearchFixture(t, options.WorkDir, "sub/modified.txt", "after\nnew\n")
	writeSearchFixture(t, options.WorkDir, "outside.txt", "outside after\n")
	writeSearchFixture(t, options.WorkDir, "sub/untracked.txt", "a\r\nb\rc\n")
	writeSearchFixture(t, options.WorkDir, "sub/empty.txt", "")
	writeSearchFixture(t, options.WorkDir, "sub/binary.bin", string([]byte{0, 1, 2}))
	id := createTestSession(t, host, filepath.Join(options.WorkDir, "sub"))
	base := "/api/sessions/" + id + "/workspace/"
	status := requestJSON(t, host, "GET", base+"status", "", 200)
	var result struct {
		State        string
		Branch       string
		IsGitRepo    bool
		ChangedFiles []workspaceChange
	}
	if err := json.Unmarshal(status, &result); err != nil || result.State != "ok" || result.Branch != "fixture" || !result.IsGitRepo || len(result.ChangedFiles) != 4 {
		t.Fatal(string(status), err)
	}
	byPath := map[string]workspaceChange{}
	for _, change := range result.ChangedFiles {
		byPath[change.Path] = change
	}
	if byPath["modified.txt"].Additions != 2 || byPath["modified.txt"].Deletions != 1 || byPath["untracked.txt"].Additions != 3 {
		t.Fatal(string(status))
	}
	for _, path := range []string{"modified.txt", "untracked.txt", "binary.bin", "empty.txt"} {
		diff := requestJSON(t, host, "GET", base+"diff?path="+url.QueryEscape(path), "", 200)
		text := stringField(historyRecord(diff), "diff")
		if stringField(historyRecord(diff), "state") != "ok" || text == "" {
			t.Fatal(path, string(diff))
		}
		if path == "binary.bin" && !strings.Contains(text, "Binary files /dev/null and b/binary.bin differ\n") {
			t.Fatal(text)
		}
		if path == "empty.txt" && !strings.Contains(text, "@@ -0,0 +1,1 @@\n+\n") {
			t.Fatal(text)
		}
		if path == "modified.txt" && !strings.Contains(text, "+after\n+new\n") {
			t.Fatal(text)
		}
	}
	missing := requestJSON(t, host, "GET", base+"diff?path=missing.txt", "", 200)
	if stringField(historyRecord(missing), "state") != "missing" {
		t.Fatal(string(missing))
	}
}
