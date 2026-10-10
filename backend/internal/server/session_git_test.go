package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionGitInfoUsesNativeGitAndOriginalLaunchBranch(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Environment = func() map[string]string {
		return map[string]string{"HOME": options.HomeDir, "USERPROFILE": options.HomeDir, "CLAUDE_CONFIG_DIR": options.ConfigDir, "PATH": os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(options.HomeDir, "absent-git-config"), "GIT_OPTIONAL_LOCKS": "0"}
	}
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	type result struct {
		Branch       *string
		RepoName     string
		WorkDir      string
		ChangedFiles int
		Worktree     map[string]json.RawMessage
	}
	var before result
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/git-info", "", 200)
	if json.Unmarshal(raw, &before) != nil || before.Branch != nil || before.RepoName != "work" || before.WorkDir != options.WorkDir || before.ChangedFiles != 0 || before.Worktree != nil {
		t.Fatal(string(raw))
	}
	for _, path := range []string{filepath.Join(options.WorkDir, ".git", "objects"), filepath.Join(options.WorkDir, ".git", "refs", "heads")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, contents := range map[string]string{"HEAD": "ref: refs/heads/fixture\n", "config": "[core]\n repositoryformatversion = 0\n bare = false\n[remote \"origin\"]\n url = git@github.com:fixture-owner/real-repo.git\n"} {
		if err := os.WriteFile(filepath.Join(options.WorkDir, ".git", path), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(options.WorkDir, "actual-untracked.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/git-info", "", 200)
	var cached result
	if json.Unmarshal(raw, &cached) != nil || cached.RepoName != "work" || cached.ChangedFiles != 0 {
		t.Fatal("Source negative lookup was not cached", string(raw))
	}
	server, host = testHost(t, options)
	var actual result
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/git-info", "", 200)
	if json.Unmarshal(raw, &actual) != nil || actual.RepoName != "real-repo" || actual.ChangedFiles != 1 || actual.WorkDir != options.WorkDir || actual.Branch != nil {
		t.Fatal("actual git metadata/status differs", string(raw))
	}
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"type": "session-meta", "repository": map[string]any{"worktree": true, "branch": "selected-base", "worktreePath": options.WorkDir, "repoRoot": options.WorkDir, "requestedWorkDir": options.WorkDir, "worktreeBranch": "generated-branch", "worktreeSlug": "fixture-slug", "future": true}})
	appendRows(t, info.Path, string(meta))
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/git-info", "", 200)
	if json.Unmarshal(raw, &actual) != nil || actual.Branch == nil || *actual.Branch != "selected-base" || stringField(actual.Worktree, "path") != options.WorkDir || stringField(actual.Worktree, "slug") != "fixture-slug" || stringField(actual.Worktree, "branch") != "generated-branch" {
		t.Fatal("Source worktree launch priorities differ", string(raw))
	}
	requestJSON(t, host, "POST", "/api/sessions/"+id+"/git-info", "{}", 405)
}
