package clibootstrap

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeTmuxFastPathLargePRRoundedRefPhysicalGitAndTmux(t *testing.T) {
	f, _, runTmux := privateTmuxFastFixture(t)
	f.n.Environment["GIT_AUTHOR_NAME"] = "fixture"
	f.n.Environment["GIT_AUTHOR_EMAIL"] = "fixture@example.invalid"
	f.n.Environment["GIT_COMMITTER_NAME"] = "fixture"
	f.n.Environment["GIT_COMMITTER_EMAIL"] = "fixture@example.invalid"
	f.write(t, filepath.Join(f.cwd, "tracked.txt"), "private rounded PR exact bytes")
	runGit := func(args ...string) {
		t.Helper()
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: f.cwd, Environment: f.n.Environment})
		if e != nil || r.Code != 0 {
			t.Fatal(args, r, e)
		}
	}
	runGit("init", "-b", "main")
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "private initial")
	runGit("update-ref", "refs/pull/9007199254740992/head", "HEAD")
	origin := filepath.Join(f.home, "origin.git")
	runGit("clone", "--bare", f.cwd, origin)
	runGit("--git-dir", origin, "update-ref", "refs/pull/9007199254740992/head", "refs/heads/main")
	runGit("remote", "add", "origin", origin)
	var out, stderr bytes.Buffer
	result, e := RunNativeTmuxWorktree(context.Background(), []string{"--tmux=classic", "--worktree=#9007199254740993", "-c", "sleep 30"}, f.n, runtime.GOOS, "/bin/sh", TmuxFastPathIO{Input: strings.NewReader(""), Output: &out, Error: &stderr})
	if e != nil || !result.Handled || result.Error != "" {
		t.Fatal(result, e, out.String(), stderr.String())
	}
	canonical, e := filepath.EvalSymlinks(f.cwd)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(canonical, ".claude/worktrees/pr-9007199254740992")
	raw, e := os.ReadFile(filepath.Join(path, "tracked.txt"))
	if e != nil || string(raw) != "private rounded PR exact bytes" {
		t.Fatal(string(raw), e)
	}
	actual := runTmux("display-message", "-p", "-t", "project_worktree-pr-9007199254740992", "#{pane_current_path}")
	if actual.Code != 0 || strings.TrimSpace(actual.Stdout) != path {
		t.Fatal("real tmux PR cwd", actual)
	}
	if _, e = os.Stat(filepath.Join(f.config, "projects")); !os.IsNotExist(e) {
		t.Fatal("outer fastpath initialized transcript", e)
	}
}
