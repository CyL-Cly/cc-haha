package clibootstrap

// Regress rejected task_started publication before the background child owns
// its lifetime. Business observations precede explicitly labelled fixture-only
// cleanup, so rejected output must settle its real registration and Join.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func backgroundStartFailureDirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestActualBackgroundAgentStartOutputFailureCompensatesBeforeTransfer(t *testing.T) {
	for _, row := range []struct{ name, priorAlias string }{{"new-name", ""}, {"restore-prior-name", "a0123456789abcdef"}} {
		t.Run(row.name, func(t *testing.T) { backgroundAgentStartOutputFailureCase(t, row.priorAlias) })
	}
}

func backgroundAgentStartOutputFailureCase(t *testing.T, priorAlias string) {
	fixture := worktreeMainFixture(t)
	// The existing clean-worktree regression removes this include file too:
	// otherwise copying its deliberately untracked secret fixture is a real
	// worktree modification, and retaining the worktree would be correct.
	if err := os.Remove(filepath.Join(fixture.cwd, ".worktreeinclude")); err != nil {
		t.Fatal(err)
	}
	fixture.write(t, filepath.Join(fixture.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"maxConcurrentSubagents":1,"enabledPlugins":{"sample@mkt":false}}`)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if count := requests.Add(1); count != 1 {
			t.Errorf("unexpected additional actual child model request: %d", count)
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			emitPlanFixture(writer, 1, "", "NEXT_STARTUP_COMPENSATED_RESULT")
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(unblock)
	fixture.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	fixture.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	fixture.n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	fixture.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	fixture.n.Environment["CLAUDE_CODE_TMPDIR"] = fixture.n.Temp
	sentinel := errors.New("owned task_started output rejected before transfer")
	var outputMu sync.Mutex
	var failedID string
	var failedJoin func(context.Context) error
	var captureErr error
	var publishingScope *cooperative.Scope
	var m *MainPrint
	starts := 0
	output := func(data []byte) error {
		var event protocol.JSONObject
		if err := event.UnmarshalJSON(data); err != nil {
			return err
		}
		if event.String("type") == "system" && event.String("subtype") == "task_started" && event.String("task_type") == "local_agent" {
			outputMu.Lock()
			starts++
			first := starts == 1
			if first {
				failedID = event.String("task_id")
				// Take the actual pre-publication consumer handle while this
				// same owner scope is current. No registry field is changed.
				if publishingScope == nil {
					captureErr = errors.New("task start was published outside its actual owning call")
				} else {
					entry, err := m.Controller.BackgroundTaskOnOwner(publishingScope, failedID)
					captureErr = err
					if entry != nil {
						failedJoin = entry.Join
					}
				}
			}
			outputMu.Unlock()
			if first {
				return sentinel
			}
		}
		return nil
	}
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual startup compensation parent", Tools: []string{"Agent,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: fixture.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: output})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	// Only test fixture retirement may remove an observed failed registration.
	// It is deliberately separate from all business observations below and
	// never alters the concurrency counter or fabricates a completed task.
	removeObservedOrphan := func() error {
		outputMu.Lock()
		id := failedID
		outputMu.Unlock()
		if id == "" {
			return nil
		}
		cleanupContext, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return m.Owner.Run(cleanupContext, func(scope *cooperative.Scope) error {
			entry, err := m.Controller.BackgroundTaskOnOwner(scope, id)
			if err != nil || entry == nil {
				return err
			}
			t.Logf("fixture-only retirement after business observation: deleting rejected startup task %s (status %s)", id, entry.Data.String("status"))
			entry.Cancel(context.Canceled)
			return m.Controller.DeleteBackgroundTaskOnOwner(scope, id)
		})
	}
	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func() error {
		shutdownOnce.Do(func() {
			unblock()
			if err := removeObservedOrphan(); err != nil {
				t.Errorf("fixture-only rejected-task cleanup: %v", err)
			}
			closeContext, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- m.ShutdownSession(closeContext) }()
			select {
			case shutdownErr = <-done:
			case <-closeContext.Done():
				shutdownErr = context.Cause(closeContext)
			}
		})
		return shutdownErr
	}
	t.Cleanup(func() {
		if err := shutdown(); err != nil {
			t.Errorf("owned fixture failed bounded ShutdownSession; no signal fallback: %v", err)
		}
	})
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	parent := m.state.Snapshot()
	worktrees := filepath.Join(fixture.cwd, ".claude", "worktrees")
	admin := filepath.Join(fixture.cwd, ".git", "worktrees")
	beforeWorktrees := backgroundStartFailureDirectoryNames(t, worktrees)
	beforeAdmin := backgroundStartFailureDirectoryNames(t, admin)
	const name = "startup-compensation-worker"
	var request builtintools.AgentRequest
	var startErr error
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		// Use the existing real name-registration API for a preexisting alias;
		// no fake task/history/model result is inserted for that fixture state.
		if priorAlias != "" {
			if err := m.Controller.RegisterAgentNameOnOwner(scope, name, priorAlias); err != nil {
				return err
			}
		}
		if prior, err := m.Controller.ResolveAgentNameOnOwner(scope, name); err != nil || prior != priorAlias {
			if err != nil {
				return err
			}
			return errors.New("private fixture unexpectedly has an existing agent name")
		}
		var err error
		request, err = actualBackgroundAgentRequest(scope, m, "Return the exact next startup result")
		if err != nil {
			return err
		}
		request.Isolation, request.Name = "worktree", name
		publishingScope = scope
		defer func() { publishingScope = nil }()
		_, startErr = m.runBackgroundAgent(ctx, scope, request)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(startErr, sentinel) {
		t.Fatalf("startup lost actual output failure identity: got=%v want=%v", startErr, sentinel)
	}
	outputMu.Lock()
	id := failedID
	join := failedJoin
	observationErr := captureErr
	outputMu.Unlock()
	if len(id) < 8 || join == nil || observationErr != nil {
		t.Fatal("real task_started output did not expose its registered identity/join", id, observationErr)
	}
	if requests.Load() != 0 {
		t.Errorf("rejected pre-transfer startup opened %d model requests", requests.Load())
	}
	joinContext, stopJoin := context.WithTimeout(context.Background(), 100*time.Millisecond)
	joinErr := join(joinContext)
	stopJoin()
	if joinErr != nil {
		t.Errorf("rejected startup left its actual consumer Join unclosed after returning: %v", joinErr)
	}
	// All RED assertions precede the fixture-only deletion. The old production
	// registers task/name before Emit and therefore should fail here for the
	// intended business reason, rather than waiting on its never-closed Join.
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if entry != nil && entry.Data.String("status") == "running" {
			t.Errorf("rejected task_started left a running registration before lifetime transfer: %s", rawMain(entry.Data))
		}
		resolved, err := m.Controller.ResolveAgentNameOnOwner(scope, name)
		if err != nil {
			return err
		}
		if resolved != priorAlias {
			t.Errorf("rejected task_started failed to restore its name binding: name=%s got=%s want=%s", name, resolved, priorAlias)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := m.state.Snapshot(); got.CWD != parent.CWD || got.OriginalCWD != parent.OriginalCWD || got.ProjectRoot != parent.ProjectRoot || m.worktreeState.current != nil {
		t.Error("failed child startup retargeted the actual parent workspace", parent, got)
	}
	if _, err := os.Stat(filepath.Join(worktrees, "agent-"+id[:8])); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rejected startup retained its clean physical worktree: %v", err)
	}
	if got := backgroundStartFailureDirectoryNames(t, worktrees); !reflect.DeepEqual(got, beforeWorktrees) {
		t.Errorf("failed startup changed worktree membership: before=%v after=%v", beforeWorktrees, got)
	}
	if got := backgroundStartFailureDirectoryNames(t, admin); !reflect.DeepEqual(got, beforeAdmin) {
		t.Errorf("failed startup left Git worktree registration: before=%v after=%v", beforeAdmin, got)
	}
	if err := removeObservedOrphan(); err != nil {
		t.Fatal("fixture-only cleanup after recorded business assertions", err)
	}
	var launched builtintools.AgentRunResult
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := m.runBackgroundAgent(ctx, scope, request)
		if err != nil {
			return err
		}
		launched, err = future.Await(scope)
		return err
	}); err != nil {
		t.Fatal("failed startup did not release its real admission slot", err)
	}
	if !launched.Async || launched.AgentID == "" || launched.AgentID == id {
		t.Fatal("next admission did not create an independent actual background task", launched)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("next admitted child never reached actual loopback model", context.Cause(ctx))
	}
	// A failed startup must release exactly once, not leave the shared domain
	// negative and allow a third child while the one permitted child is busy.
	var limited error
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, limited = m.runBackgroundAgent(ctx, scope, request)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if limited == nil || !strings.Contains(limited.Error(), "Maximum concurrent subagents reached (1)") {
		t.Errorf("actual one-slot domain failed to reject third admission: %v", limited)
	}
	unblock()
	terminal := joinTaskPublicationProducer(t, m, ctx, launched.AgentID)
	if terminal.String("status") != "completed" || !strings.Contains(string(terminal.Get("result")), "NEXT_STARTUP_COMPENSATED_RESULT") || requests.Load() != 1 {
		t.Errorf("next actual child did not complete after failed-startup compensation: task=%s requests=%d", rawMain(terminal), requests.Load())
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		resolved, err := m.Controller.ResolveAgentNameOnOwner(scope, name)
		if err == nil && resolved != launched.AgentID {
			return errors.New("next admitted child did not own the actual registered name")
		}
		return err
	}); err != nil {
		t.Error(err)
	}
	if got := backgroundStartFailureDirectoryNames(t, worktrees); !reflect.DeepEqual(got, beforeWorktrees) {
		t.Errorf("next completed clean child retained worktree membership: before=%v after=%v", beforeWorktrees, got)
	}
	if got := backgroundStartFailureDirectoryNames(t, admin); !reflect.DeepEqual(got, beforeAdmin) {
		t.Errorf("next completed clean child retained Git registration: before=%v after=%v", beforeAdmin, got)
	}
	if err := shutdown(); err != nil {
		t.Fatal("actual parent did not finish bounded ShutdownSession after fixture cleanup", err)
	}
}
