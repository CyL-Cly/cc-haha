package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateGit(t *testing.T) (*Service, string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "repo")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home, "PATH": "/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(home, "absent-gitconfig"), "GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_AUTHOR_DATE": "2026-10-01T00:00:00Z", "GIT_COMMITTER_DATE": "2026-10-01T00:00:00Z", "GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": ""}
	s, e := New(Options{Files: OSFiles{}, Environment: env, GitExecutable: NativeGitExecutable(env), Run: NativeRun, Now: func() time.Time { return time.UnixMilli(1000) }, Log: func(level, message string) { t.Log(level, message) }})
	if e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) {
		t.Helper()
		result, e := s.run(context.Background(), root, args, true)
		if e != nil || result.Code != 0 {
			t.Fatal(args, result, e)
		}
	}
	run("init", "-b", "main")
	if e = os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("private tracked file"), 0644); e != nil {
		t.Fatal(e)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "initial private fixture")
	return s, root, env
}
func TestRealGitCreateResumeKeepSafetySparseAndRemove(t *testing.T) {
	s, root, _ := privateGit(t)
	ctx := context.Background()
	_ = os.MkdirAll(filepath.Join(root, ".claude"), 0700)
	_ = os.WriteFile(filepath.Join(root, ".claude/settings.local.json"), []byte(`{"future":"keep","env":{"fake":"value"}}`), 0600)
	_ = os.MkdirAll(filepath.Join(root, "node_modules"), 0700)
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules\n.claude/settings.local.json\n"), 0644)
	_, _ = s.run(ctx, root, []string{"add", ".gitignore"}, false)
	_, _ = s.run(ctx, root, []string{"commit", "-m", "ignore private setup outputs"}, false)
	r := Request{CWD: root, SessionID: "11111111-1111-4111-8111-111111111111", Slug: "user/feature", Settings: Settings{SymlinkDirectories: []string{"node_modules"}}}
	created, e := s.Create(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	if created.Path != filepath.Join(root, ".claude/worktrees/user+feature") || created.Branch != "worktree-user+feature" || created.OriginalHeadCommit == "" || created.CreationDurationMs == nil {
		t.Fatal("actual creation state", created)
	}
	copied, e := os.ReadFile(filepath.Join(created.Path, ".claude/settings.local.json"))
	if e != nil || string(copied) != `{"future":"keep","env":{"fake":"value"}}` {
		t.Fatal("local settings lost", string(copied), e)
	}
	st, e := os.Stat(filepath.Join(created.Path, ".claude/settings.local.json"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("source file mode lost", st, e)
	}
	link, e := os.Readlink(filepath.Join(created.Path, "node_modules"))
	if e != nil || link != filepath.Join(root, "node_modules") {
		t.Fatal("real symlink absent", link, e)
	}
	resumed, e := s.Create(ctx, r)
	if e != nil || resumed.CreationDurationMs != nil || resumed.OriginalHeadCommit != created.OriginalHeadCommit {
		t.Fatal("actual existing .git/HEAD fast resume", resumed, e)
	}
	if code, e := s.ValidateRemove(ctx, &created, false); e != nil || code != 0 {
		t.Fatal("clean working tree refused", code, e)
	}
	_ = os.WriteFile(filepath.Join(created.Path, "new.txt"), []byte("must preserve"), 0600)
	if code, e := s.ValidateRemove(ctx, &created, false); e == nil || code != 2 || !strings.Contains(e.Error(), "uncommitted") {
		t.Fatal("dirty worktree was not refused", code, e)
	}
	if code, e := s.ValidateRemove(ctx, &created, true); e != nil || code != 0 {
		t.Fatal("explicit discard not accepted", code, e)
	}
	removed, e := s.Remove(ctx, created)
	if e != nil || !removed {
		t.Fatal(removed, e)
	}
	if _, e = os.Stat(created.Path); !os.IsNotExist(e) {
		t.Fatal("actual worktree remains", e)
	}
	result, e := s.run(ctx, root, []string{"show-ref", "--verify", "refs/heads/" + created.Branch}, false)
	if e != nil || result.Code == 0 {
		t.Fatal("actual worktree branch remains", result, e)
	}
	// No active session is an explicit model-visible no-op; it cannot delete a
	// manually-created or previous-session worktree.
	if code, e := s.ValidateRemove(ctx, nil, false); e == nil || code != 1 {
		t.Fatal("foreign state admitted", code, e)
	}
}
func TestGitWorktreeRejectsSymlinkAndSparseFailureTearsDown(t *testing.T) {
	s, root, _ := privateGit(t)
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, ".claude")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(context.Background(), Request{CWD: root, SessionID: "fixture", Slug: "safe"}); e == nil || !strings.Contains(e.Error(), "symbolic link") {
		t.Fatal("managed symlink escaped", e)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("outside directory was mutated")
	}
	_ = os.Remove(filepath.Join(root, ".claude"))
	r := Request{CWD: root, SessionID: "fixture", Slug: "sparse", Settings: Settings{SparsePaths: []string{"../invalid"}}}
	if _, e := s.Create(context.Background(), r); e == nil {
		t.Fatal("invalid sparse path accepted")
	}
	if _, e := os.Stat(PathFor(root, r.Slug)); !os.IsNotExist(e) {
		t.Fatal("half-created sparse tree survived", e)
	}
}
