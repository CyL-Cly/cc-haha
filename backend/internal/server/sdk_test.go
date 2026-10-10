package server

import (
	"context"
	"encoding/json"
	"golang.org/x/net/websocket"
	"strings"
	"sync"
	"testing"
	"time"
)

// This fixture covers credentials/socket identity only; the application test
// exercises actual shared native ASK, real Write, and separate permission IDs.
type sdkTransportFixture struct {
	mu    sync.Mutex
	send  func(json.RawMessage) error
	epoch uint64
	seen  chan json.RawMessage
	onSDK func(json.RawMessage)
}

func (f *sdkTransportFixture) Handle(context.Context, json.RawMessage) error { return nil }
func (f *sdkTransportFixture) Snapshot(context.Context) ([]json.RawMessage, error) {
	return []json.RawMessage{}, nil
}
func (f *sdkTransportFixture) Close(context.Context) error { return nil }
func (f *sdkTransportFixture) AcceptSDKOutput(_ context.Context, data json.RawMessage) error {
	f.onSDK(data)
	f.seen <- data
	return nil
}
func (f *sdkTransportFixture) BindSDKResponseTransport(send func(json.RawMessage) error) func() {
	f.mu.Lock()
	f.epoch++
	epoch := f.epoch
	f.send = send
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		if f.epoch == epoch {
			f.send = nil
		}
		f.mu.Unlock()
	}
}

func TestDedicatedSDKCredentialsReplayReconnectRevocationAndShutdown(t *testing.T) {
	opened := make(chan *sdkTransportFixture, 4)
	opts := make(chan SessionOptions, 4)
	options := testOptions(t, func(_ context.Context, o SessionOptions, _ Emit) (LiveSession, error) {
		f := &sdkTransportFixture{seen: make(chan json.RawMessage, 20), onSDK: o.OnSDK}
		opened <- f
		opts <- o
		return f, nil
	})
	s, host := testHost(t, options)
	s.setCollaborationEndpoint(host.Listener.Addr())
	id := createTestSession(t, host, options.WorkDir)
	if _, e := s.live(id); e != nil {
		t.Fatal(e)
	}
	f := <-opened
	o := <-opts
	token := o.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"]
	dial := func(id, token string) *websocket.Conn {
		t.Helper()
		ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/sdk/"+id+"?token="+token, "", host.URL)
		if e != nil {
			t.Fatal(e)
		}
		_ = ws.SetDeadline(time.Now().Add(5 * time.Second))
		t.Cleanup(func() { _ = ws.Close() })
		return ws
	}
	closed := func(ws *websocket.Conn) {
		t.Helper()
		var data string
		if e := websocket.Message.Receive(ws, &data); e == nil {
			t.Fatal("invalid or revoked SDK credential accepted", data)
		}
	}
	closed(dial(id, ""))
	closed(dial(id, "wrong"))
	closed(dial("foreign", token))
	first := dial(id, token)
	if e := websocket.Message.Send(first, `invalid`+"\n"+`{"type":"progress","uuid":"replay","data":"original"}`); e != nil {
		t.Fatal(e)
	}
	select {
	case data := <-f.seen:
		if !strings.Contains(string(data), "original") {
			t.Fatal(string(data))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SDK output missing")
	}
	second := dial(id, token)
	_ = first.Close()
	if e := websocket.Message.Send(second, `{"type":"progress","uuid":"replay","data":"duplicate"}`+"\n"+`{"type":"progress","uuid":"barrier","data":"new"}`); e != nil {
		t.Fatal(e)
	}
	select {
	case data := <-f.seen:
		if !strings.Contains(string(data), "barrier") {
			t.Fatal("reconnect replay reached consumer", string(data))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new SDK output missing")
	}
	if len(s.sdkCapture(id).Recent()) != 2 {
		t.Fatal("SDK duplicate retained")
	}
	// Detaching the old peer must not detach the newest response transport.
	f.mu.Lock()
	send := f.send
	f.mu.Unlock()
	if send == nil {
		t.Fatal("old peer unbound new socket")
	}
	if e := send(json.RawMessage(`{"type":"control_request","request_id":"native-control","request":{"subtype":"interrupt"}}`)); e != nil {
		t.Fatal(e)
	}
	var response string
	if e := websocket.Message.Receive(second, &response); e != nil || !strings.HasSuffix(response, "\n") {
		t.Fatal("SDK line framing", e, response)
	}
	if e := s.stopSessionAndWait(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	closed(second)
	if _, e := s.live(id); e != nil {
		t.Fatal(e)
	}
	next := <-opened
	nextOpt := <-opts
	nextToken := nextOpt.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"]
	if token == nextToken || len(s.sdkCapture(id).Recent()) != 0 {
		t.Fatal("new owned runtime retained old credential/capture")
	}
	closed(dial(id, token))
	fresh := dial(id, nextToken)
	if e := websocket.Message.Send(fresh, `{"type":"progress","uuid":"replay","data":"fresh runtime"}`); e != nil {
		t.Fatal(e)
	}
	select {
	case data := <-next.seen:
		if !strings.Contains(string(data), "fresh runtime") {
			t.Fatal(string(data))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new runtime replay identity was not reset")
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	o.OnSDK(json.RawMessage(`{"type":"result","is_error":false}`))
	s.sdk.mu.Lock()
	retained := len(s.sdk.captures)
	s.sdk.mu.Unlock()
	if retained != 0 {
		t.Fatal("SDK capture recreated after shutdown")
	}
	closed(fresh)
}
