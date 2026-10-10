package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func awaitReceived(t *testing.T, received <-chan json.RawMessage) json.RawMessage {
	t.Helper()
	select {
	case raw := <-received:
		return raw
	case <-time.After(2 * time.Second):
		t.Fatal("shared session did not receive the expected frame")
		return nil
	}
}

func TestAdmissionPreservesSocketUserOrderWhilePermissionAndCancelBypass(t *testing.T) {
	live := &transportSession{received: make(chan json.RawMessage, 40), blocked: make(chan struct{})}
	options := testOptions(t, func(_ context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
		live.emit = emit
		return live, nil
	})
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := dial(t, host, id)
	for range 3 {
		receive(t, socket)
	}
	if err := websocket.Message.Send(socket, `{"type":"user_message","content":"first"}`); err != nil {
		t.Fatal(err)
	}
	if stringField(objectForTest(awaitReceived(t, live.received)), "content") != "first" {
		t.Fatal("first user frame changed")
	}
	for index := range 32 {
		if err := websocket.Message.Send(socket, fmt.Sprintf(`{"type":"user_message","content":"queued-%02d","future":{"index":%d}}`, index, index)); err != nil {
			t.Fatal(err)
		}
	}
	permission := `{"type":"permission_response","requestId":"pending","behavior":"allow","future":{"intact":true}}`
	if err := websocket.Message.Send(socket, permission); err != nil {
		t.Fatal(err)
	}
	if got := awaitReceived(t, live.received); string(got) != permission {
		t.Fatalf("a queued user frame overtook the permission control: %s", got)
	}
	if err := websocket.Message.Send(socket, `{"type":"stop_generation"}`); err != nil {
		t.Fatal(err)
	}
	if stringField(objectForTest(awaitReceived(t, live.received)), "type") != "stop_generation" {
		t.Fatal("cancel was queued behind the active turn")
	}
	close(live.blocked)
	for index := range 32 {
		got := objectForTest(awaitReceived(t, live.received))
		if stringField(got, "content") != fmt.Sprintf("queued-%02d", index) || string(got["future"]) != fmt.Sprintf(`{"index":%d}`, index) {
			t.Fatalf("user frame %d admitted out of socket order or lost unknown fields: %s", index, got)
		}
	}
}

func objectForTest(raw json.RawMessage) map[string]json.RawMessage {
	var result map[string]json.RawMessage
	_ = json.Unmarshal(raw, &result)
	return result
}

func TestShutdownCancelsAdmissionAndDropsQueuedUserFrames(t *testing.T) {
	live := &transportSession{received: make(chan json.RawMessage, 3), blocked: make(chan struct{})}
	options := testOptions(t, func(_ context.Context, _ SessionOptions, _ Emit) (LiveSession, error) { return live, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := dial(t, host, id)
	for range 3 {
		receive(t, socket)
	}
	if err := websocket.Message.Send(socket, `{"type":"user_message","content":"active"}`); err != nil {
		t.Fatal(err)
	}
	awaitReceived(t, live.received)
	if err := websocket.Message.Send(socket, `{"type":"user_message","content":"must-not-run"}`); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Send(socket, `{"type":"ping"}`); err != nil {
		t.Fatal(err)
	}
	if stringField(objectForTest(receive(t, socket)), "type") != "pong" {
		t.Fatal("socket reader did not reach the admission barrier")
	}
	runtime, err := server.runtime(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.admitted:
	default:
		t.Fatal("server shutdown returned before the admission worker exited")
	}
	select {
	case raw := <-live.received:
		t.Fatalf("queued frame survived shutdown: %s", raw)
	default:
	}
	if live.closed.Load() != 1 {
		t.Fatal("owned live session did not close once")
	}
}
