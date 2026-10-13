package imruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestBridgeIdleReleaseKeepsTurnBackgroundApprovalAndRestoresSameSID(t *testing.T) {
	home := t.TempDir()
	connections := make(chan *websocket.Conn, 4)
	var count atomic.Int32
	serverSocket := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		count.Add(1)
		connections <- conn
		defer conn.Close()
		for {
			var message []byte
			if websocket.Message.Receive(conn, &message) != nil {
				return
			}
		}
	}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			serverSocket.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/sessions/same-sid" {
			_, _ = w.Write(raw(map[string]string{"id": "same-sid", "workDir": home, "permissionMode": "default"}))
			return
		}
		w.WriteHeader(404)
	}))
	defer peer.Close()
	cfg := Config{Home: home, ConfigDir: filepath.Join(home, "relocated"), WorkDir: home, Roots: []string{home}, ServerURL: "ws" + strings.TrimPrefix(peer.URL, "http"), Environment: map[string]string{}}
	var clock atomic.Int64
	clock.Store(1)
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	bridge := NewBridge(context.Background(), cfg, nil, func(string) {}, BridgeOptions{Now: now, HeartbeatInterval: time.Hour})
	defer bridge.Close()
	store := NewSessionStore(cfg.ConfigDir)
	if err := store.Set("chat", "same-sid", home); err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 20)
	handler := func(_ context.Context, m protocol.JSONObject) error { seen <- m.String("type"); return nil }
	bridge.Connect("chat", "same-sid", handler)
	if !bridge.WaitOpen(context.Background(), "chat") {
		t.Fatal("loopback did not open")
	}
	conn := <-connections
	send := func(message string) {
		t.Helper()
		if err := websocket.Message.Send(conn, []byte(message)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-seen:
		case <-time.After(time.Second):
			t.Fatal("inbound state was not tracked")
		}
	}
	advance := func() { clock.Add(int64((IdleSessionRelease + time.Second) / time.Millisecond)) }
	retained := func() {
		t.Helper()
		advance()
		if released := bridge.ReleaseIdleSessions(); len(released) != 0 {
			t.Fatal("busy or approval binding released", released)
		}
	}
	send(`{"type":"status","state":"thinking"}`)
	retained()
	send(`{"type":"status","state":"idle"}`)
	send(`{"type":"permission_request","requestId":"approval"}`)
	retained()
	send(`{"type":"permission_resolved","requestId":"approval"}`)
	send(`{"type":"system_notification","subtype":"session_state_changed","data":{"state":"running"}}`)
	retained()
	send(`{"type":"system_notification","subtype":"session_state_changed","data":{"state":"idle"}}`)
	send(`{"type":"session_state","turnState":"idle","activeBackgroundTaskIds":["task"]}`)
	retained()
	send(`{"type":"session_state","turnState":"idle","activeBackgroundTaskIds":[]}`)
	advance()
	bridge.MarkActive("chat")
	if len(bridge.ReleaseIdleSessions()) != 0 {
		t.Fatal("marked traffic raced idle sweep")
	}
	advance()
	if !bridge.Send("chat", object([]byte(`{"type":"ping"}`))) {
		t.Fatal("heartbeat send failed")
	}
	if released := bridge.ReleaseIdleSessions(); !reflect.DeepEqual(released, []string{"chat"}) {
		t.Fatal("heartbeat kept idle Agent alive", released)
	}
	if bridge.HasSession("chat") || store.Get("chat").SessionID != "same-sid" {
		t.Fatal("release removed durable binding or retained WS")
	}
	client := NewHTTPClient(cfg, peer.Client())
	restored, err := RestoreSession(context.Background(), "chat", store, client, bridge, handler, func() {})
	if err != nil || restored.Status != "restored" || bridge.SessionID("chat") != "same-sid" {
		t.Fatal("next traffic changed SID", restored, err)
	}
	<-connections
	if count.Load() != 2 {
		t.Fatal("restoration did not reopen physical WS", count.Load())
	}
}
