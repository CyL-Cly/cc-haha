package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type connectPermissionSession struct {
	transportSession
	sdkOnlyRunning bool
}

func (s *connectPermissionSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	state := "idle"
	if s.sdkOnlyRunning {
		state = "running"
	}
	return []json.RawMessage{
		rawDisconnect(map[string]any{"type": "session_state", "turnState": state, "activeBackgroundTaskIds": []string{}}),
		json.RawMessage(`{"type":"permission_request","requestId":"retained-write","toolName":"Write","input":{}}`),
		rawDisconnect(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{"retained-write"}, "computerUseRequestIds": []string{}, "turnActive": s.sdkOnlyRunning}),
	}, nil
}

func TestClientConnectionReplaysPermissionsAndOnlySyncRequestsTurnState(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "warm-with-pending-permission"}[warm], func(t *testing.T) {
			live := &connectPermissionSession{transportSession: transportSession{received: make(chan json.RawMessage, 1)}, sdkOnlyRunning: warm}
			options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return live, nil })
			_, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			if warm {
				first := dial(t, host, id)
				if err := websocket.Message.Send(first, `{"type":"prewarm_session"}`); err != nil {
					t.Fatal(err)
				}
				awaitReceived(t, live.received)
				_ = first.Close()
			}
			socket := dial(t, host, id)
			if err := websocket.Message.Send(socket, `{"type":"ping"}`); err != nil {
				t.Fatal(err)
			}
			var connected []string
			for {
				frame := objectForTest(receive(t, socket))
				kind := stringField(frame, "type")
				if kind == "permission_requests_snapshot" && string(frame["turnActive"]) != "false" {
					t.Fatal("SDK-only running snapshot replaced the foreground registration", frame)
				}
				connected = append(connected, kind)
				if kind == "pong" {
					break
				}
			}
			want := `["connected","permission_requests_snapshot","pong"]`
			if warm {
				want = `["connected","permission_request","permission_requests_snapshot","pong"]`
			}
			if string(mustJSON(t, connected)) != want {
				t.Fatalf("connect produced an unsolicited authoritative state or lost approvals: %v", connected)
			}
			if err := websocket.Message.Send(socket, `{"type":"sync_state"}`); err != nil {
				t.Fatal(err)
			}
			if err := websocket.Message.Send(socket, `{"type":"ping"}`); err != nil {
				t.Fatal(err)
			}
			state := objectForTest(receive(t, socket))
			if stringField(state, "type") != "session_state" || stringField(state, "turnState") != "idle" {
				t.Fatal("explicit sync did not return foreground state", state)
			}
			if next := objectForTest(receive(t, socket)); stringField(next, "type") != "pong" {
				t.Fatal("explicit state sync replayed connect-only approvals", next)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStopRevokesAllOldQueuedInputsAndAllowsOnlyFreshReplacement(t *testing.T) {
	for _, startup := range []bool{false, true} {
		name := "active"
		if startup {
			name = "startup"
		}
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			live := &transportSession{received: make(chan json.RawMessage, 16), blocked: make(chan struct{})}
			options := testOptions(t, func(ctx context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
				live.emit = emit
				close(entered)
				if startup {
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return live, nil
			})
			server, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			first := dial(t, host, id)
			receiveType(t, first, "connected")
			if err := websocket.Message.Send(first, `{"type":"user_message","content":"active-old"}`); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("actual input did not enter factory")
			}
			if !startup {
				if first := objectForTest(awaitReceived(t, live.received)); stringField(first, "content") != "active-old" {
					t.Fatal("first input did not execute", first)
				}
			}
			for _, content := range []string{"queued-old-one", "queued-old-two", "queued-old-three"} {
				if err := websocket.Message.Send(first, `{"type":"user_message","content":"`+content+`"}`); err != nil {
					t.Fatal(err)
				}
			}
			if err := websocket.Message.Send(first, `{"type":"ping"}`); err != nil {
				t.Fatal(err)
			}
			receiveType(t, first, "pong")
			runtime, err := server.runtime(id)
			if err != nil {
				t.Fatal(err)
			}
			runtime.mu.Lock()
			active := runtime.activeUserTurn
			queued := append([]admissionFrame(nil), runtime.admission...)
			runtime.mu.Unlock()
			if active == nil || len(queued) != 3 {
				t.Fatal("socket did not register the suspended inputs", active, len(queued))
			}
			second := dial(t, host, id)
			receiveType(t, second, "connected")
			if err := websocket.Message.Send(second, `{"type":"stop_generation"}`); err != nil {
				t.Fatal(err)
			}
			select {
			case <-active.ctx.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("Stop did not cancel active registration")
			}
			// Cancellation signals the active context before finishing the queue
			// loop. Acquire its owning mutex to observe the entire Stop fence.
			runtime.mu.Lock()
			runtime.mu.Unlock()
			for _, frame := range queued {
				if frame.userTurn == nil || frame.userTurn.ctx.Err() == nil {
					t.Fatalf("Stop retained old queued admission identity: %s", frame.raw)
				}
			}
			observer := dial(t, host, id)
			receiveType(t, observer, "connected")
			if err := websocket.Message.Send(observer, `{"type":"sync_state"}`); err != nil {
				t.Fatal(err)
			}
			if state := objectForTest(receiveType(t, observer, "session_state")); stringField(state, "turnState") != "idle" {
				t.Fatal("revoked foreground remained active while Stop waited on its runtime", state)
			}
			if startup {
				close(release)
			}
			if stopped := objectForTest(awaitReceived(t, live.received)); stringField(stopped, "type") != "stop_generation" {
				t.Fatal("old user executed instead of actual Stop", stopped)
			}
			if err := websocket.Message.Send(second, `{"type":"user_message","content":"fresh-replacement"}`); err != nil {
				t.Fatal(err)
			}
			if fresh := objectForTest(awaitReceived(t, live.received)); stringField(fresh, "content") != "fresh-replacement" {
				t.Fatal("an old queued input survived Stop", fresh)
			}
			close(live.blocked)
		})
	}
}
