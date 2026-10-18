package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// Only the actual final output's delivery is held. The same native Host,
// Owner, projection, Tea input reader and global writer remain the consumers.
type expandedViewOutputSession struct {
	*Session
	release     chan struct{}
	releaseOnce sync.Once
	ready       chan struct{}
	sending     chan struct{}
	turns       chan error
	holdOnQueue bool
	commitStart chan struct{}
	commitDone  chan error
}

type observedExpandedViewCommit struct {
	tui.TerminalExpandedViewCommit
	started chan struct{}
	done    chan error
}

func (r observedExpandedViewCommit) Wait() error {
	close(r.started)
	err := r.TerminalExpandedViewCommit.Wait()
	r.done <- err
	return err
}

func (s *expandedViewOutputSession) releaseOutput() { s.releaseOnce.Do(func() { close(s.release) }) }

func (s *expandedViewOutputSession) SetTerminalExpandedView(ctx context.Context, value string) error {
	s.releaseOutput()
	<-s.sending
	return s.Session.SetTerminalExpandedView(ctx, value)
}

func (s *expandedViewOutputSession) EnqueueTerminalExpandedView(ctx context.Context, value string) (tui.TerminalExpandedViewCommit, error) {
	receipt, err := s.Session.EnqueueTerminalExpandedView(ctx, value)
	if !s.holdOnQueue {
		s.releaseOutput()
	}
	if err == nil && s.commitStart != nil {
		return observedExpandedViewCommit{receipt, s.commitStart, s.commitDone}, nil
	}
	return receipt, err
}

func TestActualTerminalExpandedViewPendingReceiptJoinsOnCancelledTerminalClose(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		calls.Add(1)
		response(w, "expanded-view-close-output", "", nil, "EXPANDED_VIEW_CLOSE_DONE")
	}))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.OnSDK = func(data json.RawMessage) error {
		if obj(data).String("type") == "result" {
			close(ready)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	wrapper := &expandedViewOutputSession{Session: s, release: make(chan struct{}), holdOnQueue: true, commitStart: make(chan struct{}), commitDone: make(chan error, 1), turns: make(chan error, 1)}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- tui.Run(ctx, wrapper, tui.Options{Input: reader, Output: &commandMenuOutput{}, InitialPrompt: "EXPANDED_VIEW_CLOSE", Width: 100, Height: 36, Environment: []string{"TERM=dumb", "TERM_PROGRAM=Apple_Terminal"}})
	}()
	defer cancel()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("Actual final output never reached its close boundary")
	}
	if _, err := writer.Write([]byte("\x14")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wrapper.commitStart:
	case <-time.After(2 * time.Second):
		t.Fatal("Actual queued receipt did not enter its owned background wait")
	}
	select {
	case err := <-wrapper.commitDone:
		t.Fatal("Receipt completed while its actual Owner output was held", err)
	default:
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Actual terminal close lost cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Actual terminal close retained the pending expanded-view receipt")
	}
	select {
	case err := <-wrapper.commitDone:
		if err != nil && !errors.Is(err, cooperative.ErrClosed) {
			t.Fatal("Actual queued writer failed outside its cancellation/close boundary", err)
		}
	default:
		t.Fatal("Terminal returned before joining its actual commit receipt")
	}
	if calls.Load() != 1 {
		t.Fatal("Closing the view writer started another model", calls.Load())
	}
	if _, err := s.EnqueueTerminalExpandedView(context.Background(), "tasks"); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("Closed Owner admitted a new view write", err)
	}
}

func (s *expandedViewOutputSession) Submit(ctx context.Context, input tui.Input, emit tui.Emit) error {
	err := s.Session.Submit(ctx, input, emit)
	s.turns <- err
	return err
}

func TestActualTerminalExpandedViewCommitConsumesHeldOwnerOutputThenNextHuman(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		n := calls.Add(1)
		if n == 2 && (!bytes.Contains(body, []byte("EXPANDED_VIEW_FIRST")) || !bytes.Contains(body, []byte("EXPANDED_VIEW_NEXT"))) {
			t.Errorf("Actual next Harness lost the committed input history: %s", body)
		}
		if n > 2 {
			t.Errorf("Expanded-view persistence started another model: %s", body)
		}
		response(w, "expanded-view-output", "", nil, "EXPANDED_VIEW_OUTPUT_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	ready, release, sending := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	o.OnSDK = func(data json.RawMessage) error {
		if obj(data).String("type") == "result" {
			once.Do(func() { close(ready); <-release; close(sending) })
		}
		return nil
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"hasCompletedOnboarding":true,"showExpandedTodos":false,"expandedFuture":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	wrapper := &expandedViewOutputSession{Session: s, release: release, ready: ready, sending: sending, turns: make(chan error, 2)}
	defer wrapper.releaseOutput()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &commandMenuOutput{}
	finished := make(chan error, 1)
	go func() {
		finished <- tui.Run(ctx, wrapper, tui.Options{Input: reader, Output: output, InitialPrompt: "EXPANDED_VIEW_FIRST", Width: 100, Height: 36, Environment: []string{"TERM=dumb", "TERM_PROGRAM=Apple_Terminal"}})
	}()
	defer func() {
		cancel()
		wrapper.releaseOutput()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("Actual terminal did not join the output/view receipt during close")
		}
	}()
	select {
	case <-wrapper.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("Actual Owner never reached the held final output", output.text())
	}
	input := make(chan error, 1)
	go func() { _, err := writer.Write([]byte("\x14")); input <- err }()
	select {
	case err := <-input:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Actual Tea input reader stopped")
	}
	select {
	case err := <-wrapper.turns:
		if err != nil {
			t.Fatal("Actual held output did not complete", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tea's committed expanded-view wait blocked the same Owner's actual output", output.text())
	}
	waitGlobal := func(expanded bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			data, err := os.ReadFile(path)
			value := obj(data)
			if err == nil && (string(value.Get("showExpandedTodos")) == "true") == expanded && string(obj(value.Get("expandedFuture")).Get("keep")) == "true" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("Actual committed global write or unknown field missing", expanded, err, string(data))
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitGlobal(true)
	if _, err := writer.Write([]byte("\x14EXPANDED_VIEW_NEXT\r")); err != nil {
		t.Fatal(err)
	}
	waitGlobal(false)
	select {
	case err := <-wrapper.turns:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Next actual same-Harness input was blocked", output.text())
	}
	if calls.Load() != 2 {
		t.Fatal("Expanded-view transition replaced or added a model turn", calls.Load())
	}
	if _, err := s.EnqueueTerminalExpandedView(ctx, "foreign"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Unknown expanded view admitted")
	}
}
