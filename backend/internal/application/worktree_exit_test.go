package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func TestWorktreeActualSessionExitKeepDirtyAndRemoveClean(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{true: "keep-dirty", false: "remove-clean"}[dirty], func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:9")
			o.Native.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
			o.Native.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(o.Environment["HOME"], "absent-gitconfig")
			for key, value := range map[string]string{"GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"} {
				o.Native.Environment[key] = value
			}
			if e := os.WriteFile(filepath.Join(o.WorkDir, "tracked.txt"), []byte("private tracked"), 0600); e != nil {
				t.Fatal(e)
			}
			for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "private initial"}} {
				result, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: o.Native.CWD, Environment: o.Native.Environment, IgnoreStdin: true})
				if e != nil || result.Code != 0 {
					t.Fatal(args, result, e)
				}
			}
			name := "exit-private"
			o.Arguments.Worktree = &worktree.Startup{Name: &name}
			s, e := NewSession(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = s.Close(context.Background()) })
			path := worktree.PathFor(o.Native.CWD, name)
			if dirty {
				if e = os.WriteFile(filepath.Join(path, "proof.txt"), []byte("preserved private file"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			prompt, e := s.PrepareWorktreeExit(context.Background())
			if e != nil || prompt == nil {
				t.Fatal(prompt, e)
			}
			if (len(prompt.ChangedFiles) > 0) != dirty {
				t.Fatal("actual status wasn't read", prompt)
			}
			choice := "remove"
			if dirty {
				choice = "keep"
			}
			message, e := s.CompleteWorktreeExit(context.Background(), choice)
			if e != nil {
				t.Fatal(e)
			}
			if dirty {
				data, e := os.ReadFile(filepath.Join(path, "proof.txt"))
				if e != nil || string(data) != "preserved private file" || !strings.Contains(message, "Worktree kept.") {
					t.Fatal("dirty keep", message, string(data), e)
				}
			} else {
				if _, e = os.Stat(path); !os.IsNotExist(e) || message != "Worktree removed (no changes)" {
					t.Fatal("clean physical remove", message, e)
				}
			}
		})
	}
}
