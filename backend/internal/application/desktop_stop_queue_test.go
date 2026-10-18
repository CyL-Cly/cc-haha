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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

type desktopAdmissionObservedContext struct {
	context.Context
	checks chan error
}

func (ctx desktopAdmissionObservedContext) Err() error {
	err := ctx.Context.Err()
	ctx.checks <- err
	return err
}

func TestDesktopRevokedUserCannotStageThinkingAfterAdmissionLocks(t *testing.T) {
	for _, boundary := range []string{"entry", "admission", "runtime"} {
		t.Run(boundary, func(t *testing.T) {
			var calls, thinking atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "revoked user must not request a model", 400)
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			live, err := DesktopFactory(o)(context.Background(), server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(frame json.RawMessage) {
				if message := obj(frame); message.String("type") == "status" && message.String("state") == "thinking" {
					thinking.Add(1)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer live.Close(context.Background())
			desktop := live.(*DesktopSession)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks := make(chan error, 16)
			observed := desktopAdmissionObservedContext{Context: ctx, checks: checks}
			var unlock func()
			if boundary == "entry" {
				cancel()
			} else if boundary == "admission" {
				desktop.admission.Lock()
				unlock = desktop.admission.Unlock
			} else {
				desktop.runtimeMu.Lock()
				unlock = desktop.runtimeMu.Unlock
			}
			defer func() {
				if unlock != nil {
					unlock()
				}
			}()
			done := make(chan error, 1)
			go func() {
				done <- live.Handle(observed, raw(map[string]any{"type": "user_message", "content": "revoked input"}))
			}()
			if boundary != "entry" {
				count := 1
				if boundary == "runtime" {
					count = 2
				}
				for range count {
					select {
					case err := <-checks:
						if err != nil {
							t.Fatal("input was revoked before reaching suspended admission", err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("input did not reach its admission boundary")
					}
				}
				cancel()
				unlock()
				unlock = nil
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("revoked admission did not settle as cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("revoked admission remained suspended")
			}
			if thinking.Load() != 0 || calls.Load() != 0 {
				t.Fatal("revoked admission changed the visible or actual model lifecycle", thinking.Load(), calls.Load())
			}
		})
	}
}

func TestActualDesktopStopDropsOldQueuedUsersBeforeFreshNativeWrite(t *testing.T) {
	opened, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if bytes.Contains(body, []byte("OLD_QUEUED_MUST_NEVER_EXECUTE")) {
			t.Error("Stop allowed an old queued user into the actual model request")
		}
		switch calls.Add(1) {
		case 1:
			close(opened)
			<-r.Context().Done()
			close(canceled)
		case 2:
			if !bytes.Contains(body, []byte("FRESH_AFTER_STOP")) {
				t.Error("replacement user did not reach the shared native Harness")
			}
			response(w, "fresh-write", "Write", map[string]any{"file_path": target, "content": "FRESH_PHYSICAL_WRITE"}, "")
		case 3:
			if !bytes.Contains(body, []byte("fresh-write")) {
				t.Error("native Write result missing from next actual request")
			}
			response(w, "fresh-complete", "", nil, "FRESH_AFTER_STOP_COMPLETE")
		default:
			t.Error("unexpected extra model request after Stop")
			http.Error(w, "unexpected request", 400)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "acceptEdits"
	target = filepath.Join(o.WorkDir, "fresh-after-stop.txt")
	s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(s)
	t.Cleanup(func() { _ = s.Close(context.Background()); host.Close() })
	request, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "acceptEdits"})))
	request.Header.Set("Content-Type", "application/json")
	result, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(result.Body)
	_ = result.Body.Close()
	id := obj(body).String("sessionId")
	if result.StatusCode != 201 || id == "" {
		t.Fatal("public session creation failed", result.StatusCode, string(body))
	}
	socket, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	_ = socket.SetDeadline(time.Now().Add(20 * time.Second))
	send := func(value any) {
		t.Helper()
		if err := websocket.Message.Send(socket, string(raw(value))); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(kind string) json.RawMessage {
		t.Helper()
		for range 100 {
			var frame string
			if err := websocket.Message.Receive(socket, &frame); err != nil {
				t.Fatal(err)
			}
			message := obj([]byte(frame))
			if message.String("type") == "error" {
				t.Fatal("actual desktop failure", frame)
			}
			if message.String("type") == kind {
				return json.RawMessage(frame)
			}
		}
		t.Fatal("missing actual desktop frame", kind)
		return nil
	}
	receive("connected")
	send(map[string]any{"type": "user_message", "content": "active request that the user will stop"})
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("first actual model request did not start")
	}
	for range 3 {
		send(map[string]any{"type": "user_message", "content": "OLD_QUEUED_MUST_NEVER_EXECUTE"})
	}
	send(map[string]any{"type": "ping"})
	receive("pong")
	send(map[string]any{"type": "stop_generation"})
	send(map[string]any{"type": "ping"})
	receive("pong")
	select {
	case <-canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not cancel the actual pending HTTP request")
	}
	send(map[string]any{"type": "user_message", "content": "FRESH_AFTER_STOP"})
	reply := ""
	for !strings.Contains(reply, "FRESH_AFTER_STOP_COMPLETE") {
		reply += obj(receive("content_delta")).String("text")
	}
	receive("message_complete")
	requestDesktopSessionSync(t, socket)
	assertDesktopIdleSnapshot(t, obj(receive("session_state")))
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "FRESH_PHYSICAL_WRITE" {
		t.Fatal("fresh native Write did not produce its physical artifact", err, string(contents))
	}
	if calls.Load() != 3 {
		t.Fatal("old queue or replacement caused unexpected requests", calls.Load())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal("same native runtime did not close and join", err)
	}
}
