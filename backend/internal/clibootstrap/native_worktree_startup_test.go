package clibootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func TestWorktreeStartupBindsProjectBeforeAgentSkillAndPromptDiscovery(t *testing.T) {
	for _, named := range []bool{true, false} {
		t.Run(map[bool]string{true: "named", false: "plan-slug"}[named], func(t *testing.T) {
			f := worktreeMainFixture(t)
			// Starting in a main-repository subdirectory retains that originalCwd.
			f.n.CWD = filepath.Join(f.cwd, "src")
			name := "private-startup"
			startup := &worktree.Startup{}
			if named {
				startup.Name = &name
			}
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Worktree: startup, ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"EnterWorktree,ExitWorktree,Read"}, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if m.worktreeState == nil || m.worktreeState.current == nil {
				t.Fatal("startup didn't create actual worktree")
			}
			session := m.worktreeState.current
			if session.OriginalCWD != filepath.Join(f.cwd, "src") {
				t.Fatal("main subdirectory identity changed", session.OriginalCWD)
			}
			state := m.state.Snapshot()
			if state.CWD != session.Path || state.OriginalCWD != session.Path || state.ProjectRoot != session.Path || m.options.Native.CWD != session.Path {
				t.Fatal("startup project graph identity", state, m.options.Native.CWD)
			}
			if named && session.Name != name {
				t.Fatal("provided name changed", session.Name)
			}
			if !named {
				if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					slug, e := m.plans.Slug(scope)
					if e == nil && slug != session.Name {
						t.Error("startup slug replaced", slug, session.Name)
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			if m.nativeSettings.n.CWD != session.Path {
				t.Fatal("discovery still bound to original repo")
			}
			if got := m.Controller.Writer().Snapshot().String("path"); got != "" {
				t.Fatal("startup orphan transcript created", got)
			}
			if _, e = os.Stat(filepath.Join(session.Path, "src", "tracked.txt")); e != nil {
				t.Fatal("actual sparse checkout", e)
			}
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			if m.workingDirectory() != session.Path {
				t.Fatal("host initialization reset startup cwd")
			}
		})
	}
}
