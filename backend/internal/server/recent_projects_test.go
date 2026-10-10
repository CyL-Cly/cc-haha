package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRecentProjectsUsesSourceLogicalRootsScanDepthAndRealGitMetadata(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	repo := filepath.Join(options.WorkDir, "repo")
	for _, path := range []string{filepath.Join(repo, ".git", "objects"), filepath.Join(repo, ".git", "refs", "heads"), filepath.Join(repo, "src"), filepath.Join(options.WorkDir, "other")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, contents := range map[string]string{"HEAD": "ref: refs/heads/fixture\n", "config": "[core]\n repositoryformatversion = 0\n bare = false\n[remote \"origin\"]\n url = git@github.com:fixture-owner/fixture-repo.git\n"} {
		if err := os.WriteFile(filepath.Join(repo, ".git", path), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := createTestSession(t, host, repo)
	second := createTestSession(t, host, filepath.Join(repo, "src"))
	third := createTestSession(t, host, filepath.Join(options.WorkDir, "other"))
	for index, id := range []string{first, second, third} {
		info, err := server.findSession(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		appendRows(t, info.Path, `{"type":"user","timestamp":"2026-10-05T0`+string(rune('1'+index))+`:00:00.000Z","message":{"role":"user","content":"fixture"}}`)
	}
	var result struct{ Projects []recentProject }
	if err := json.Unmarshal(requestJSON(t, host, "GET", "/api/sessions/recent-projects?limit=1suffix", "", 200), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Projects) != 1 || result.Projects[0].ProjectName != "other" {
		t.Fatal("Source parseInt limit/order changed")
	}
	if err := json.Unmarshal(requestJSON(t, host, "GET", "/api/sessions/recent-projects?limit=20&scan=5000", "", 200), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Projects) != 2 {
		t.Fatalf("project grouping changed: %+v", result.Projects)
	}
	project := result.Projects[1]
	if project.RealPath != canonicalPath(repo) || project.SessionCount != 2 || !project.IsGit || project.RepoName == nil || *project.RepoName != "fixture-owner/fixture-repo" || project.Branch != nil {
		t.Fatalf("real Git read/logical root changed: %+v", project)
	}
	server.mu.Lock()
	cache := server.recentProjects
	server.mu.Unlock()
	if cache == nil || cache.scan != 5000 {
		t.Fatal("deep scan was answered by a shallow cached result")
	}
	fourth := createTestSession(t, host, options.WorkDir)
	server.mu.Lock()
	cache = server.recentProjects
	server.mu.Unlock()
	if fourth == "" || cache != nil {
		t.Fatal("creation did not invalidate recent project cache")
	}
	requestJSON(t, host, "GET", "/api/sessions/recent-projects?limit=0&scan=garbage", "", 200)
}

func TestRecentRemoteOwnerAndGeneratedWorktreeNamesMatchSource(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repository.git", "https://github.com/owner/repository.git", "ssh://git@github.com/owner/repository"} {
		got := remoteOwnerName(&remote)
		if got == nil || *got != "owner/repository" {
			t.Fatalf("remote=%s owner=%v", remote, got)
		}
	}
	if recentName(`/workspace/repo/.claude/worktrees/desktop-branch`, "fallback") != "repo" || recentName(`C:\workspace\repo`, "fallback") != "repo" {
		t.Fatal("worktree project display name changed")
	}
}
