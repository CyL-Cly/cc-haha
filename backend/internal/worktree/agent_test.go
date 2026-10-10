package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualAgentWorktreeCanonicalResumeTimestampChangesAndCleanup(t *testing.T) {
	s, root, _ := privateGit(t)
	s.options.TouchAgentWorktree = func(path string, now time.Time) error { return os.Chtimes(path, now, now) }
	ctx := context.Background()
	parent, e := s.Create(ctx, Request{CWD: root, SessionID: "real-parent", Slug: "parent"})
	if e != nil {
		t.Fatal(e)
	}
	attempt, e := s.CreateAgent(ctx, parent.Path, "agent-a1234567", Settings{})
	if e != nil || attempt.Worktree == nil {
		t.Fatal(attempt, e)
	}
	info := *attempt.Worktree
	physicalRoot, _ := filepath.EvalSymlinks(root)
	if info.GitRoot != physicalRoot || info.Path != PathFor(physicalRoot, "agent-a1234567") {
		t.Fatal("nested agent worktree", info)
	}
	if e = os.Chtimes(info.Path, time.Unix(0, 0), time.Unix(0, 0)); e != nil {
		t.Fatal(e)
	}
	resumed, e := s.CreateAgent(ctx, parent.Path, "agent-a1234567", Settings{})
	st, _ := os.Stat(info.Path)
	if e != nil || resumed.Worktree == nil || resumed.Worktree.HeadCommit != info.HeadCommit || st.ModTime().UnixMilli() != 1000 {
		t.Fatal("actual fast resume timestamp", resumed, st, e)
	}
	if changed, e := s.HasAgentChanges(ctx, info); e != nil || changed {
		t.Fatal("clean", changed, e)
	}
	_ = os.WriteFile(filepath.Join(info.Path, "output.txt"), []byte("agent private output"), 0600)
	if changed, e := s.HasAgentChanges(ctx, info); e != nil || !changed {
		t.Fatal("dirty", changed, e)
	}
	removed, e := s.RemoveAgent(ctx, info)
	if e != nil || !removed {
		t.Fatal(removed, e)
	}
	if _, e = os.Stat(info.Path); !os.IsNotExist(e) {
		t.Fatal("physical dir remains", e)
	}
	if _, e = os.Stat(parent.Path); e != nil {
		t.Fatal("parent deleted", e)
	}
	plain := t.TempDir()
	attempt, e = s.CreateAgent(ctx, plain, "invalid/../slug", Settings{})
	if e != nil || attempt.Worktree != nil || attempt.UnavailableReason != AgentUnavailableReason {
		t.Fatal("Source softened only missingVCS prerequisite", attempt, e)
	}
	s.options.Run = func(context.Context, ProcessRequest) (ProcessResult, error) {
		return ProcessResult{Code: 1, Stderr: "git failed"}, nil
	}
	if changed, e := s.HasAgentChanges(ctx, info); e != nil || !changed {
		t.Fatal("git failure must preserve", changed, e)
	}
	if removed, e := s.RemoveAgent(ctx, AgentWorktree{Path: parent.Path}); e != nil || removed {
		t.Fatal("missing root deleted", removed, e)
	}
}
