package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func TestWorktreeResumeActualSourceExistingMissingAndFreshPrecedence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("explicit Source Bun")
	}
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-worktree-resume.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WORKTREE_RESUME_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source resume %v %.3000s", e, stderr.String())
	}
	type observed struct {
		Cwd, Original, Project, Bytes string
		Current                       *worktree.Session
	}
	var source struct {
		Existing, Exited, Missing, Fresh observed
		Root                             string
		Capabilities                     struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.3000s", e, output)
	}
	for _, scenario := range []string{"existing", "missing", "fresh"} {
		t.Run(scenario, func(t *testing.T) {
			f := worktreeMainFixture(t)
			m := worktreeOnlyMain(t, f)
			bindings, e := m.nativeWorktreeBindings()
			if e != nil {
				t.Fatal(e)
			}
			saved, e := bindings.Service.Create(ctx, worktree.Request{CWD: f.cwd, SessionID: m.state.SessionID(), Slug: "private/feature"})
			if e != nil {
				t.Fatal(e)
			}
			var target *worktree.Session = &saved
			if scenario == "missing" {
				copy := saved
				copy.Path = filepath.Join(f.cwd, "missing-worktree")
				target = &copy
			}
			if scenario == "fresh" {
				fresh, e := bindings.Service.Create(ctx, worktree.Request{CWD: f.cwd, SessionID: m.state.SessionID(), Slug: "fresh-worktree"})
				if e != nil {
					t.Fatal(e)
				}
				if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					m.worktreeState.current = &fresh
					if e := m.setWorkingDirectory(scope, fresh.Path); e != nil {
						return e
					}
					if e := m.state.SetOriginalCWD(scope, m.workingDirectory()); e != nil {
						return e
					}
					return m.state.SetProjectRoot(scope, m.workingDirectory())
				}); e != nil {
					t.Fatal(e)
				}
			}
			value, _ := json.Marshal(target)
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.nativeRestoreWorktreeForResume(scope, value, false) }); e != nil {
				t.Fatal(e)
			}
			want := source.Existing
			if scenario == "missing" {
				want = source.Missing
			}
			if scenario == "fresh" {
				want = source.Fresh
			}
			current := m.state.Snapshot()
			norm := func(s string) string { return strings.ReplaceAll(s, source.Root, f.cwd) }
			if current.CWD != norm(want.Cwd) || current.OriginalCWD != norm(want.Original) || current.ProjectRoot != norm(want.Project) {
				t.Errorf("Source identity %+v Go%+v", want, current)
			}
			if (m.worktreeState.current == nil) != (want.Current == nil) {
				t.Fatal("Source current ownership differs")
			}
			if want.Current != nil && m.worktreeState.current.Name != want.Current.Name {
				t.Fatal("Source precedence differs", m.worktreeState.current.Name, want.Current.Name)
			}
			metadata := m.Controller.Writer().Snapshot()
			var cached protocol.JSONObject
			_ = cached.UnmarshalJSON(metadata.Get("metadata"))
			if scenario == "missing" && string(cached.Get("worktreeSession")) != "null" {
				t.Fatal("missing state not cleared")
			}
		})
	}
}
