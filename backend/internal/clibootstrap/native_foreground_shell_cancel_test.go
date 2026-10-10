package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualForegroundBashCancellationDropsUIAndJoinsPhysicalProcess(t *testing.T) {
	var command string
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		emitNativeTaskTool(w, "actual-cancel-foreground-bash", "Bash", map[string]any{"command": command, "description": "actual canceled foreground shell"})
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = true })
	marker := filepath.Join(m.options.Native.CWD, "canceled-process-must-not-finish")
	command = "sleep 10; printf SHOULD_NOT_FINISH > '" + marker + "'"
	states := make(chan bool, 8)
	_, remove, e := m.SubscribeForegroundTasks(context.Background(), func(v bool) { states <- v })
	if e != nil {
		t.Fatal(e)
	}
	defer remove()
	turn, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(turn, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run actual cancellable foreground Bash"})}))
	}()
	select {
	case v := <-states:
		if !v {
			t.Fatal("missing real foreground")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("registration timed out")
	}
	cancel()
	select {
	case <-done:
		// The retained Stop action may close the parent ASK with a nil
		// receipt; no new inference or detached command may survive it.
	case <-time.After(8 * time.Second):
		t.Fatal("cancel did not settle real Harness")
	}
	select {
	case v := <-states:
		if v {
			t.Fatal("canceled physical shell kept Task context")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("physical completion did not clear foreground UI")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("canceled process completed unowned", e)
	}
	if requests.Load() != 1 {
		t.Fatal("canceled shell entered new model loop", requests.Load())
	}
}

func TestActualShortInteractiveBashCompletesWithoutForegroundRegistration(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "actual-short-bash", "Bash", map[string]any{"command": "printf ACTUAL_SHORT_FG_BASH_ND21", "description": "actual short foreground shell"})
			return
		}
		if !strings.Contains(string(body), "ACTUAL_SHORT_FG_BASH_ND21") {
			t.Errorf("real short output lost %.1400s", body)
		}
		emitPlanFixture(w, int(n), "", "SHORT_ACTUAL_DONE")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.ForegroundShellProgress = true })
	states := make(chan bool, 8)
	_, remove, e := m.SubscribeForegroundTasks(context.Background(), func(v bool) { states <- v })
	if e != nil {
		t.Fatal(e)
	}
	defer remove()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run short Bash"})})); e != nil {
		t.Fatal(e)
	}
	select {
	case value := <-states:
		t.Fatal("short Source task registered foreground", value)
	default:
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
		if len(tasks) != 0 {
			t.Fatalf("short task leaked registry %s", rawMain(tasks))
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
