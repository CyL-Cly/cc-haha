package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualRestoredRemoteHeldPollAllowsNextOrdinaryHarnessHTTP(t *testing.T) {
	started, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sessions/session-owned-held" {
			_, _ = w.Write([]byte(`{"session_status":"running"}`))
			return
		}
		if r.URL.Path != "/v1/sessions/session-owned-held/events" {
			t.Error("unowned API", r.URL)
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			close(joined)
			return
		}
		_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
		close(joined)
	}))
	modelHTTP := make(chan string, 1)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelHTTP <- string(body)
		response(w, "remote-poll-overlap", "", nil, "HELD_REMOTE_NEXT_HUMAN_COMPLETE")
	}))
	defer model.Close()
	o := terminalRemoteTaskOptions(t, model.URL, proxy, ca)
	seedTerminalRemoteTask(t, o, "held")
	o.Arguments.SessionID = ""
	o.Arguments.Resume = remoteTaskResumeSID
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	owner, controller, host := s.process.Owner, s.process.Controller, s.process.Host
	if err = s.StartTerminalRemoteTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("actual restored remote poll missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Submit(ctx, tui.Input{Text: "ORDINARY_HUMAN_WHILE_REMOTE_POLL_HELD"}, nil) }()
	select {
	case body := <-modelHTTP:
		if !strings.Contains(body, "ORDINARY_HUMAN_WHILE_REMOTE_POLL_HELD") || !strings.Contains(body, "OLD_REMOTE_TASK_RESUME_CONTEXT") {
			t.Fatal("actual same-owner model input/history lost", body)
		}
	case err := <-done:
		t.Fatal("ordinary ASK returned before real model HTTP", err)
	case <-ctx.Done():
		t.Fatal("held remote poll serialized unrelated ordinary ASK")
	}
	select {
	case <-joined:
		t.Fatal("ordinary human canceled remote poll")
	default:
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if s.process.Owner != owner || s.process.Controller != controller || s.process.Host != host {
		t.Fatal("remote poll replaced the actual Harness")
	}
	close(release)
}
