package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type closingSession struct {
	transportSession
	closeStarted chan struct{}
	closeRelease chan struct{}
}

func (session *closingSession) Close(ctx context.Context) error {
	session.closed.Add(1)
	if session.closeStarted != nil {
		close(session.closeStarted)
	}
	if session.closeRelease != nil {
		select {
		case <-session.closeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func pending(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("close returned before owned session cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}
func receiveError(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not complete")
	}
}
func TestShutdownWaitsForInFlightFactoryAndConcurrentClose(t *testing.T) {
	started := make(chan struct{})
	factoryRelease := make(chan struct{})
	factoryCancelled := make(chan struct{})
	live := &closingSession{closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	options := testOptions(t, func(ctx context.Context, _ SessionOptions, _ Emit) (LiveSession, error) {
		close(started)
		<-ctx.Done()
		close(factoryCancelled)
		<-factoryRelease
		return live, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	opening := make(chan error, 1)
	go func() { _, err := server.live(id); opening <- err }()
	<-started
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- server.Close(context.Background()) }()
	<-factoryCancelled
	go func() { second <- server.Close(context.Background()) }()
	pending(t, first)
	pending(t, second)
	close(factoryRelease)
	<-live.closeStarted
	pending(t, first)
	pending(t, second)
	close(live.closeRelease)
	receiveError(t, first)
	receiveError(t, second)
	if err := <-opening; err != context.Canceled {
		t.Fatalf("opening error=%v", err)
	}
	if live.closed.Load() != 1 {
		t.Fatalf("session closed %d times", live.closed.Load())
	}
}
func TestDisconnectRechecksActiveTurnAndReopensFromLatestHistory(t *testing.T) {
	var calls atomic.Int32
	opened := make(chan SessionOptions, 4)
	sessions := make(chan *transportSession, 4)
	options := testOptions(t, func(_ context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
		calls.Add(1)
		opened <- options
		live := &transportSession{emit: emit}
		sessions <- live
		return live, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	if _, err := server.live(id); err != nil {
		t.Fatal(err)
	}
	initial := <-opened
	live := <-sessions
	if initial.Resume {
		t.Fatal("new session unexpectedly resumed")
	}
	server.armDisconnect(id)
	runtime, _ := server.runtime(id)
	runtime.mu.Lock()
	epoch := runtime.epoch
	runtime.mu.Unlock()
	live.mu.Lock()
	live.running = true
	live.mu.Unlock()
	server.expireDisconnect(id, runtime, epoch)
	if live.closed.Load() != 0 {
		t.Fatal("active turn was evicted by stale idle timer")
	}
	live.mu.Lock()
	live.running = false
	live.mu.Unlock()
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(info.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"user","uuid":"latest-user","timestamp":"2026-10-05T00:00:00.000Z","message":{"role":"user","content":"latest persisted turn"}}` + "\n" + `{"type":"session-meta","sessionId":"` + id + `","runtimeModelId":"latest-fixture-model","runtimeProviderId":null,"future":{"keep":true}}` + "\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	server.armDisconnect(id)
	runtime.mu.Lock()
	epoch = runtime.epoch
	runtime.mu.Unlock()
	server.expireDisconnect(id, runtime, epoch)
	if live.closed.Load() != 1 {
		t.Fatal("idle runtime was not closed")
	}
	if _, err := server.live(id); err != nil {
		t.Fatal(err)
	}
	reopened := <-opened
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(reopened.Metadata, &metadata)
	if !reopened.Resume || stringField(metadata, "runtimeModelId") != "latest-fixture-model" || string(metadata["runtimeProviderId"]) != "null" || len(metadata["future"]) == 0 || calls.Load() != 2 {
		t.Fatal("reopen used stale initial session metadata/history")
	}
}
func TestDeletingSessionCancelsOpeningFactoryBeforeRemovingHistory(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	live := &closingSession{}
	options := testOptions(t, func(ctx context.Context, _ SessionOptions, _ Emit) (LiveSession, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return live, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	opening := make(chan error, 1)
	go func() { _, err := server.live(id); opening <- err }()
	<-started
	result := make(chan struct{})
	go func() { requestJSON(t, host, "DELETE", "/api/sessions/"+id, "", 200); close(result) }()
	<-cancelled
	if _, err := server.runtime(id); err != context.Canceled {
		t.Fatalf("deleting session admitted a replacement runtime: %v", err)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal("history removed before session cleanup completed")
	}
	close(release)
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("delete did not finish")
	}
	if _, err = os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatal("deleted history remains")
	}
	if err := <-opening; err != context.Canceled {
		t.Fatalf("opening error=%v", err)
	}
	if live.closed.Load() != 1 {
		t.Fatal("deleted pending runtime escaped cleanup")
	}
}

func TestShutdownHookFollowsRuntimeCleanupAndSharesError(t *testing.T) {
	live := &closingSession{closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return live, nil })
	hookStarted, hookRelease := make(chan struct{}), make(chan struct{})
	expected := errors.New("native configuration cleanup failure")
	var calls atomic.Int32
	options.OnShutdown = func(ctx context.Context) error {
		if live.closed.Load() != 1 {
			t.Error("configuration closed before runtime")
		}
		calls.Add(1)
		close(hookStarted)
		select {
		case <-hookRelease:
			return expected
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	if _, err := server.live(id); err != nil {
		t.Fatal(err)
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- server.Close(context.Background()) }()
	<-live.closeStarted
	select {
	case <-hookStarted:
		t.Fatal("native configuration released while session cleanup pending")
	default:
	}
	close(live.closeRelease)
	<-hookStarted
	go func() { second <- server.Close(context.Background()) }()
	pending(t, first)
	pending(t, second)
	close(hookRelease)
	for _, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if !errors.Is(err, expected) {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("shutdown hook remained pending")
		}
	}
	if calls.Load() != 1 || !errors.Is(server.Close(context.Background()), expected) {
		t.Fatal("shutdown hook invoked twice or lost shared error")
	}
}
