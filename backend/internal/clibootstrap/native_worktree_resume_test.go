package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func newWorktreeResumeMain(t *testing.T, f nativePluginFixture, arguments PrintArguments) *MainPrint {
	t.Helper()
	disabled := false
	arguments.ComputerUse = &disabled
	arguments.Model = "fixture-model"
	arguments.Tools = []string{"EnterWorktree,ExitWorktree,Read"}
	arguments.MaxTurns = 4
	arguments.PermissionMode = "bypassPermissions"
	arguments.Thinking = requestcontext.Thinking{Type: "disabled"}
	arguments.Sources = DefaultState()
	m, e := NewMainPrint(MainPrintOptions{Arguments: arguments, Native: f.n.agentScopedBoundary(f.n.CWD), PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestWorktreeResumeActualTranscriptExistingMissingForkAndFreshStartup(t *testing.T) {
	f := worktreeMainFixture(t)
	round := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		round++
		if round == 1 {
			emitWorktreeModel(w, round, "EnterWorktree", `{"name":"saved-worktree"}`, "")
		} else {
			emitPlanFixture(w, round, "", "private resumed fixture answer")
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	first := newWorktreeResumeMain(t, f, PrintArguments{SessionID: "11111111-1111-4111-8111-111111111111"})
	var user protocol.JSONObject
	_ = user.UnmarshalJSON([]byte(`{"type":"user","uuid":"saved-worktree-user","message":{"role":"user","content":"persist real worktree state"}}`))
	if e := first.RunTurn(context.Background(), user); e != nil {
		t.Fatal(e)
	}
	if e := first.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	paths, e := filepath.Glob(filepath.Join(f.config, "projects", "*", "11111111-1111-4111-8111-111111111111.jsonl"))
	if e != nil || len(paths) != 1 {
		t.Fatal(paths, e)
	}
	transcript := paths[0]
	baseline, e := os.ReadFile(transcript)
	if e != nil {
		t.Fatal(e)
	}
	saved := first.worktreeState.current.Path
	for _, scenario := range []string{"existing", "fork", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			if e := os.WriteFile(transcript, baseline, 0600); e != nil {
				t.Fatal(e)
			}
			arguments := PrintArguments{Resume: "11111111-1111-4111-8111-111111111111"}
			if scenario == "fork" {
				arguments.ForkSession = true
			}
			if scenario == "missing" {
				if e := os.RemoveAll(saved); e != nil {
					t.Fatal(e)
				}
			}
			m := newWorktreeResumeMain(t, f, arguments)
			state := m.state.Snapshot()
			if scenario == "existing" {
				if state.CWD != saved || state.OriginalCWD != saved || state.ProjectRoot != f.cwd || m.worktreeState.current == nil {
					t.Fatal("restore changed stable project identity", state)
				}
				if m.Controller.Writer().Snapshot().String("path") != transcript {
					t.Fatal("didn't adopt actual cross-project transcript")
				}
			}
			if scenario == "fork" {
				if state.CWD != f.cwd || m.worktreeState.current != nil || m.Controller.Writer().Snapshot().String("path") != "" {
					t.Fatal("fork took historical worktree ownership", state)
				}
			}
			if scenario == "missing" {
				if state.CWD != f.cwd || m.worktreeState.current != nil {
					t.Fatal("missing worktree retained stale state", state)
				}
				data, e := os.ReadFile(transcript)
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.Contains(data, []byte(`"worktreeSession":null`)) {
					t.Fatal("missing path not marked exited")
				}
			}
		})
	}
}
