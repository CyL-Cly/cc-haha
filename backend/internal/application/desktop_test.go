package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

// Only the remote model is a fixture: HTTP, WebSocket, permissions, shared
// Controller/ASK, native tools and the on-disk transcript are production code.
func TestDesktopTransportRunsSharedGoAgentAndReconnectsPendingPermission(t *testing.T) {
	var calls atomic.Int32
	var original string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		switch calls.Add(1) {
		case 1:
			response(w, "desktop-write", "Write", map[string]any{"file_path": original, "content": "unapproved"}, "")
		case 2:
			if !bytes.Contains(body, []byte("approved")) {
				t.Error("approved native tool result absent from model request")
			}
			response(w, "desktop-done", "", nil, "DESKTOP DONE")
		case 3:
			if !bytes.Contains(body, []byte("DESKTOP DONE")) || !bytes.Contains(body, []byte("call-desktop-write")) {
				t.Error("server restart lost real shared transcript")
			}
			response(w, "desktop-resume", "", nil, "DESKTOP RESUMED")
		default:
			t.Error("unexpected provider request")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.PermissionMode = "default"
	original = filepath.Join(o.WorkDir, "original.txt")
	approved := filepath.Join(o.WorkDir, "approved.txt")
	var opens atomic.Int32
	factory := DesktopFactory(o)
	options := server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: func(ctx context.Context, options server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
		opens.Add(1)
		return factory(ctx, options, emit)
	}}
	start := func() (*server.Server, *httptest.Server) {
		t.Helper()
		s, err := server.New(options)
		if err != nil {
			t.Fatal(err)
		}
		h := httptest.NewServer(s)
		t.Cleanup(func() { _ = s.Close(context.Background()); h.Close() })
		return s, h
	}
	s, host := start()
	request, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "default"})))
	request.Header.Set("Content-Type", "application/json")
	result, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if result.StatusCode != 201 {
		t.Fatalf("create %d %s", result.StatusCode, body)
	}
	id := obj(body).String("sessionId")
	if id == "" {
		t.Fatal("missing session ID")
	}
	dial := func(h *httptest.Server) *websocket.Conn {
		t.Helper()
		ws, err := websocket.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+id, "", h.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
		t.Cleanup(func() { _ = ws.Close() })
		return ws
	}
	send := func(ws *websocket.Conn, value any) {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(value))); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(ws *websocket.Conn, kind string) json.RawMessage {
		t.Helper()
		for count := 0; count < 100; count++ {
			var frame string
			if err := websocket.Message.Receive(ws, &frame); err != nil {
				t.Fatal(err)
			}
			m := obj([]byte(frame))
			if m.String("type") == "error" {
				t.Fatalf("real application error: %s", frame)
			}
			if m.String("type") == kind {
				return json.RawMessage(frame)
			}
		}
		t.Fatalf("missing %s", kind)
		return nil
	}
	ws := dial(host)
	receive(ws, "connected")
	send(ws, map[string]any{"type": "user_message", "content": "Write with desktop approval."})
	ask := receive(ws, "permission_request")
	permissionID := obj(ask).String("requestId")
	if permissionID == "" || obj(ask).String("toolName") != "Write" {
		t.Fatalf("permission %s", ask)
	}
	_ = ws.Close()
	ws = dial(host)
	receive(ws, "connected")
	replayed := receive(ws, "permission_request")
	if obj(replayed).String("requestId") != permissionID {
		t.Fatalf("reconnect created a different permission future: %s", replayed)
	}
	if opens.Load() != 1 {
		t.Fatalf("reconnect spawned %d harnesses", opens.Load())
	}
	send(ws, map[string]any{"type": "permission_response", "requestId": permissionID, "allowed": true, "updatedInput": map[string]any{"file_path": approved, "content": "approved"}})
	receive(ws, "message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, obj(receive(ws, "session_state")))
	if data, err := os.ReadFile(approved); err != nil || string(data) != "approved" {
		t.Fatalf("native Write result %s %v", data, err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("unapproved file created: %v", err)
	}
	_ = ws.Close()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host.Close()
	_, host = start()
	ws = dial(host)
	receive(ws, "connected")
	send(ws, map[string]any{"type": "user_message", "content": "Continue after server restart."})
	receive(ws, "message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, obj(receive(ws, "session_state")))
	if calls.Load() != 3 || opens.Load() != 2 {
		t.Fatalf("calls=%d runtimes=%d", calls.Load(), opens.Load())
	}
}
