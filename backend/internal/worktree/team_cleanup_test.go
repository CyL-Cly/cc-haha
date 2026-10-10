package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTeamWorktreeDestroyUsesRealGitRetainsBranchAndHandlesFallback(t *testing.T) {
	s, root, _ := privateGit(t)
	ctx := context.Background()
	attempt, err := s.CreateAgent(ctx, root, "agent-team-cleanup", Settings{})
	if err != nil || attempt.Worktree == nil {
		t.Fatal(err)
	}
	info := attempt.Worktree
	if err = s.DestroyTeamWorktree(ctx, info.Path, os.RemoveAll); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatal("actual worktree remains", err)
	}
	branch, err := NativeRun(ctx, ProcessRequest{Command: s.options.GitExecutable, Arguments: []string{"show-ref", "--verify", "refs/heads/" + info.Branch}, CWD: root, Environment: s.options.Environment})
	if err != nil || branch.Code != 0 {
		t.Fatal("Team cleanup changed retained branch", err, branch)
	}
	plain := filepath.Join(filepath.Dir(root), "plain-team-tree")
	if err = os.MkdirAll(plain, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(plain, "result"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.DestroyTeamWorktree(ctx, plain, os.RemoveAll); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(plain); !os.IsNotExist(err) {
		t.Fatal("fallback directory remains", err)
	}
	if err = s.DestroyTeamWorktree(ctx, plain, os.RemoveAll); err != nil {
		t.Fatal("missing directory rejected", err)
	}
}
