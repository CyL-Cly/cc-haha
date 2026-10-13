package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualIndependentForegroundAgentCancellationJoinsChildAndOneParentRegistry(t *testing.T) {
	started := make(chan struct{})
	physicalDone := make(chan struct{})
	var parents, children atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "private actual parent harness") {
			if parents.Add(1) == 1 {
				emitNativeTaskTool(w, "fg-cancel-card", "Agent", map[string]any{"description": "cancel actual foreground", "prompt": "ACTUAL_FOREGROUND_CANCEL_PRIVATE_C9"})
				return
			}
			t.Error("cancelled parent opened another model round")
			emitPlanFixture(w, 2, "", "wrong")
			return
		}
		children.Add(1)
		close(started)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(physicalDone)
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	states := make(chan bool, 8)
	_, remove, e := m.SubscribeForegroundTasks(context.Background(), func(v bool) { states <- v })
	if e != nil {
		t.Fatal(e)
	}
	defer remove()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run then cancel actual foreground"})}))
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("child never opened real request")
	}
	select {
	case v := <-states:
		if !v {
			t.Fatal("registration missing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no actual registration")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("parent never cancelled")
	}
	select {
	case <-physicalDone:
	case <-time.After(10 * time.Second):
		t.Fatal("child transport survived foreground cancellation")
	}
	select {
	case v := <-states:
		if v {
			t.Fatal("task not removed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreground finally did not remove task")
	}
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
		if e == nil && len(tasks) != 0 {
			t.Errorf("cancel left real task registry %s", rawMain(tasks))
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if e := m.ShutdownSession(shutdown); e != nil {
		t.Fatal(e)
	}
	if parents.Load() != 1 || children.Load() != 1 {
		t.Fatal("duplicate Harness requests", parents.Load(), children.Load())
	}
}

func TestActualPromotedForegroundParentShutdownJoinsSameNativeChild(t *testing.T) {
	first := make(chan struct{})
	firstClosed := make(chan struct{})
	var parents, children atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "private actual parent harness") {
			n := parents.Add(1)
			if n == 1 {
				emitNativeTaskTool(w, "fg-close-card", "Agent", map[string]any{"description": "actual promotion shutdown", "prompt": "ACTUAL_PROMOTION_SHUTDOWN_PRIVATE_Q9"})
				return
			}
			emitPlanFixture(w, int(n), "", "parent can continue")
			return
		}
		n := children.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		if n == 1 {
			close(first)
			<-r.Context().Done()
			close(firstClosed)
		} else {
			t.Error("promotion spawned a new child")
			<-r.Context().Done()
		}
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Promote then close actual native children"})}))
	}()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("foreground request absent")
	}
	changed, e := m.BackgroundForegroundAgents(context.Background())
	if e != nil || !changed {
		t.Fatal("promotion", changed, e)
	}
	select {
	case <-firstClosed:
		t.Fatal("promotion prematurely cancelled old pending request")
	default:
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("parent did not cancel")
	}
	shutdown, stop := context.WithTimeout(context.Background(), 12*time.Second)
	defer stop()
	if e = m.ShutdownSession(shutdown); e != nil {
		t.Fatal(e)
	}
	for _, done := range []<-chan struct{}{firstClosed} {
		select {
		case <-done:
		default:
			t.Fatal("shutdown released native reference before physical child stopped")
		}
	}
	if children.Load() != 1 {
		t.Fatal("spawned another Harness", children.Load())
	}
}
