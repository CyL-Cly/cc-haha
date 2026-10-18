package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type foregroundSnapshotSession struct {
	emit        Emit
	received    chan json.RawMessage
	finish      chan struct{}
	tail        chan struct{}
	completed   chan struct{}
	afterResult chan error
	once        sync.Once
	running     atomic.Bool
}

func (s *foregroundSnapshotSession) Handle(ctx context.Context, raw json.RawMessage) error {
	s.received <- append(json.RawMessage(nil), raw...)
	if stringField(objectForTest(raw), "type") != "user_message" {
		return nil
	}
	s.running.Store(true)
	select {
	case <-s.finish:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.running.Store(false)
	s.emit(json.RawMessage(`{"type":"message_complete","usage":{"input_tokens":1,"output_tokens":1}}`))
	if s.afterResult != nil {
		s.afterResult <- ctx.Err()
	}
	s.once.Do(func() { close(s.completed) })
	select {
	case <-s.tail:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *foregroundSnapshotSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	state := "idle"
	if s.running.Load() {
		state = "running"
	}
	raw, _ := json.Marshal(map[string]any{"type": "session_state", "turnState": state, "activeBackgroundTaskIds": []string{}})
	permission, _ := json.Marshal(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{}, "computerUseRequestIds": []string{}, "turnActive": s.running.Load()})
	return []json.RawMessage{raw, permission}, nil
}
func (*foregroundSnapshotSession) Close(context.Context) error { return nil }

func assertForegroundSnapshot(t *testing.T, ws *websocket.Conn, active bool) {
	t.Helper()
	state := objectForTest(receiveType(t, ws, "session_state"))
	wantState := "idle"
	if active {
		wantState = "running"
	}
	if stringField(state, "turnState") != wantState {
		t.Fatalf("registered foreground snapshot mismatched: state=%s want=%s", state, wantState)
	}
}

func assertForegroundReconnectSnapshot(t *testing.T, ws *websocket.Conn, active bool) {
	t.Helper()
	permissions := objectForTest(receiveType(t, ws, "permission_requests_snapshot"))
	wantActive := "false"
	if active {
		wantActive = "true"
	}
	if string(permissions["turnActive"]) != wantActive {
		t.Fatalf("registered connect permission snapshot mismatched: %s want=%s", permissions, wantActive)
	}
	if err := websocket.Message.Send(ws, `{"type":"sync_state"}`); err != nil {
		t.Fatal(err)
	}
	assertForegroundSnapshot(t, ws, active)
}

func TestDesktopSnapshotRegisteredUserBeforeFactoryAndSettlesBeforeCompletion(t *testing.T) {
	for _, kind := range []string{"user_message", "prewarm_session", "stop-startup"} {
		t.Run(kind, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			live := &foregroundSnapshotSession{received: make(chan json.RawMessage, 4), finish: make(chan struct{}), tail: make(chan struct{}), completed: make(chan struct{}), afterResult: make(chan error, 1)}
			options := testOptions(t, func(ctx context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
				live.emit = emit
				close(entered)
				select {
				case <-release:
					return live, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			server, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			first := dial(t, host, id)
			receiveType(t, first, "connected")
			assertForegroundReconnectSnapshot(t, first, false)
			inputKind := kind
			if kind == "stop-startup" {
				inputKind = "user_message"
			}
			if err := websocket.Message.Send(first, `{"type":"`+inputKind+`","content":"private registered turn"}`); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("actual transport did not reach suspended factory")
			}
			second := dial(t, host, id)
			receiveType(t, second, "connected")
			assertForegroundReconnectSnapshot(t, second, inputKind == "user_message")
			if kind == "stop-startup" {
				// A separate reader verifies Stop while the first connection may
				// still be waiting on the suspended shared factory.
				runtime, err := server.runtime(id)
				if err != nil {
					t.Fatal(err)
				}
				stopping := make(chan struct{})
				go func() {
					server.forwardRuntime(id, runtime, nil, json.RawMessage(`{"type":"stop_generation"}`))
					close(stopping)
				}()
				// Ping barriers are insufficient for an independently blocked
				// control. Production registration exposes its cancellation event.
				awaitRegisteredForegroundStop(t, runtime)
				if err := websocket.Message.Send(second, `{"type":"sync_state"}`); err != nil {
					t.Fatal(err)
				}
				assertForegroundSnapshot(t, second, false)
				close(release)
				select {
				case <-stopping:
				case <-time.After(3 * time.Second):
					t.Fatal("actual Stop did not settle after startup")
				}
				return
			}
			close(release)
			if input := awaitReceived(t, live.received); stringField(objectForTest(input), "type") != inputKind {
				t.Fatal("registered frame changed", string(input))
			}
			if kind == "prewarm_session" {
				return
			}
			close(live.finish)
			receiveType(t, second, "message_complete")
			if err := <-live.afterResult; err != nil {
				t.Fatal("foreground completion canceled native checkpoint/finally", err)
			}
			if err := websocket.Message.Send(second, `{"type":"sync_state"}`); err != nil {
				t.Fatal(err)
			}
			assertForegroundSnapshot(t, second, false)
			// Handle has not returned. The completion producer, rather than the
			// admission worker's finally block, owns user-turn settlement.
			close(live.tail)
		})
	}
}

func awaitRegisteredForegroundStop(t *testing.T, runtime *runtimeSession) {
	t.Helper()
	runtime.mu.Lock()
	turn := runtime.registeredUserTurn
	runtime.mu.Unlock()
	if turn == nil {
		t.Fatal("registered user missing before actual Stop")
	}
	select {
	case <-turn.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("actual Stop did not cancel registered foreground")
	}
}
