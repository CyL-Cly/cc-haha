package tui

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestForegroundTaskBindingFollowsActualAvailabilityAndPreservesReadline(t *testing.T) {
	state := NewState(nil, 80, 24)
	state.Editor.Set("abc")
	before := state.Editor.Cursor()
	actions := state.HandleKey(Key{Name: "ctrl+b"}, time.Now())
	if len(actions) != 0 || state.Editor.Cursor() != before-1 {
		t.Fatal("idle Ctrl+B lost readline", actions, state.Editor.Cursor())
	}
	state.ForegroundTasks = true
	before = state.Editor.Cursor()
	actions = state.HandleKey(Key{Name: "ctrl+b"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != "task:background" || state.Editor.Cursor() != before {
		t.Fatal("actual task availability not routed", actions)
	}
	state.ForegroundTasks = false
	state.ActiveTurn = "model-only"
	before = state.Editor.Cursor()
	actions = state.HandleKey(Key{Name: "ctrl+b"}, time.Now())
	if len(actions) != 0 || state.Editor.Cursor() != before-1 {
		t.Fatal("arbitrary model Busy was promoted", actions)
	}
}

type foregroundTestSession struct {
	*testSession
	mu       sync.Mutex
	callback func(bool)
	ready    chan struct{}
	promoted chan struct{}
	actions  atomic.Int32
	removed  atomic.Bool
}

func (s *foregroundTestSession) SubscribeForegroundTasks(_ context.Context, callback func(bool)) (bool, func(), error) {
	s.mu.Lock()
	s.callback = callback
	s.mu.Unlock()
	close(s.ready)
	return false, func() { s.mu.Lock(); s.callback = nil; s.mu.Unlock(); s.removed.Store(true) }, nil
}
func (s *foregroundTestSession) InvokeAction(_ context.Context, name string, _ Emit) error {
	if name != "task:background" {
		return &UnsupportedAction{Action: name}
	}
	s.actions.Add(1)
	select {
	case s.promoted <- struct{}{}:
	default:
	}
	return nil
}
func TestActualTerminalTaskSignalRoutesControlAndUnsubscribes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	session := &foregroundTestSession{testSession: &testSession{}, ready: make(chan struct{}), promoted: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, session, testOptions(reader, &output)) }()
	select {
	case <-session.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("not subscribed")
	}
	session.mu.Lock()
	callback := session.callback
	session.mu.Unlock()
	callback(true)
	// Two normal keys delimit the owner availability signal before Ctrl+B.
	if _, e := writer.Write([]byte("ab")); e != nil {
		t.Fatal(e)
	}
	if _, e := writer.Write([]byte{2}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-session.promoted:
	case <-time.After(3 * time.Second):
		t.Fatal("real terminal did not send background action", output.String())
	}
	callback(false)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not stop")
	}
	if !session.removed.Load() || session.actions.Load() != 1 || session.closed.Load() != 1 {
		t.Fatal("task listener or control lifecycle", session.removed.Load(), session.actions.Load(), session.closed.Load())
	}
}
