package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualForegroundAgentPromotesWithoutRestartOrToolReplay(t *testing.T) {
	pending := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	var counter string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		switch n {
		case 1:
			emitNativeTaskTool(w, "once-before-promotion", "Bash", map[string]any{"command": "printf x >> " + counter, "description": "count actual side effect"})
		case 2:
			if !strings.Contains(string(body), "once-before-promotion") {
				t.Error("same child lost executed tool history")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(pending)
			select {
			case <-release:
				emitNativeTaskTool(w, "read-after-promoted", "Read", map[string]any{"file_path": counter})
			case <-r.Context().Done():
				t.Error("promotion aborted original provider stream")
			}
		case 3:
			var request struct{ Tools []struct{ Name string } }
			_ = json.Unmarshal(body, &request)
			for _, tool := range request.Tools {
				if tool.Name == "TaskOutput" || tool.Name == "TaskStop" {
					t.Error("foreground tools remained after backgrounding", tool.Name)
				}
			}
			if !strings.Contains(string(body), "read-after-promoted") || !strings.Contains(string(body), "once-before-promotion") {
				t.Error("same iterator lost prior/pending tool continuation")
			}
			emitPlanFixture(w, 3, "", "SAME_ITERATOR_BACKGROUND_FINISHED")
		default:
			t.Error("promotion restarted/replayed query", n)
			emitPlanFixture(w, int(n), "", "UNEXPECTED_RESTART")
		}
	}))
	defer peer.Close()
	defer once.Do(func() { close(release) })
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		counter = filepath.Join(n.CWD, "promotion-counter")
		_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"maxConcurrentSubagents":1}`), 0600)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	var run builtintools.AgentRunResult
	go func() {
		done <- m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			request, e := actualBackgroundAgentRequest(scope, m, "same iterator task")
			if e != nil {
				return e
			}
			run, e = m.runAgentOnOwner(ctx, scope, request)
			return e
		})
	}()
	select {
	case <-pending:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	changed, e := m.BackgroundForegroundAgents(ctx)
	if e != nil || !changed {
		t.Fatal(changed, e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("original Agent tool did not return asynchronously", ctx.Err())
	}
	if !run.Async || run.AgentID == "" {
		t.Fatal(run)
	}
	m.options.OrdinarySubagents.mu.Lock()
	active := m.options.OrdinarySubagents.active
	m.options.OrdinarySubagents.mu.Unlock()
	if active != 1 {
		t.Fatal("promotion lost counted lease", active)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "must still reject")
		if e != nil {
			return e
		}
		_, e = m.runBackgroundAgent(ctx, scope, request)
		return e
	}); e == nil {
		t.Fatal("promotion released capacity while stream alive")
	}
	once.Do(func() { close(release) })
	for {
		m.options.OrdinarySubagents.mu.Lock()
		active = m.options.OrdinarySubagents.active
		m.options.OrdinarySubagents.mu.Unlock()
		if active == 0 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("promoted task failed to join", ctx.Err())
		}
	}
	raw, e := os.ReadFile(counter)
	if e != nil || string(raw) != "x" || calls.Load() != 3 {
		t.Fatal("replayed physical side effect", string(raw), calls.Load(), e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		task, e := m.Controller.BackgroundTaskOnOwner(scope, run.AgentID)
		if e != nil {
			return e
		}
		if task == nil || task.Data.String("status") != "completed" {
			t.Fatal(task)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
