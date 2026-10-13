//go:build !windows

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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

type desktopDeadlineTimer struct {
	callback func()
	stopped  atomic.Bool
}

func (t *desktopDeadlineTimer) Stop() bool { return !t.stopped.Swap(true) }

// Only provider HTTP is a fixture. REST, WebSocket, native ASK, Bash launch,
// cancellation/join, terminal persistence and same-session continuation are
// the production Go graph, with a manual clock instead of waiting 31 minutes.
func TestDesktopDisconnectedBashDeadlineAndStopReapRealProcess(t *testing.T) {
	for _, action := range []string{"disconnect-deadline", "stop-and-continue", "desktop-shutdown"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			var pidPath string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch n := calls.Add(1); n {
				case 1:
					response(w, "deadline-bash", "Bash", map[string]any{"command": "echo $$ > '" + strings.ReplaceAll(pidPath, "'", "'\\''") + "'; while :; do sleep 1; done", "run_in_background": true}, "")
				case 2:
					response(w, "deadline-root", "", nil, "NATIVE_BACKGROUND_ROOT_DONE")
				case 3:
					if !bytes.Contains(body, []byte("NATIVE_BACKGROUND_ROOT_DONE")) {
						t.Error("continuation lost original task history")
					}
					response(w, "deadline-continue", "", nil, "NATIVE_AFTER_STOP_DONE")
				default:
					t.Errorf("unexpected provider request %d", n)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Native = nil
			o.NativeFactory = func(environment map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: environment, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}
			pidPath = filepath.Join(o.WorkDir, "owned-background.pid")
			deadlines := make(chan *desktopDeadlineTimer, 16)
			rootResults := make(chan struct{}, 16)
			factory := DesktopFactory(o)
			s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir,
				AfterRuntimeTimer: func(delay time.Duration, callback func()) server.RuntimeTimer {
					timer := &desktopDeadlineTimer{callback: callback}
					if delay == 31*time.Minute {
						deadlines <- timer
					}
					return timer
				},
				Factory: func(ctx context.Context, options server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
					onSDK := options.OnSDK
					options.OnSDK = func(data json.RawMessage) {
						if onSDK != nil {
							onSDK(data)
						}
						if obj(data).String("type") == "result" {
							rootResults <- struct{}{}
						}
					}
					return factory(ctx, options, emit)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			host := httptest.NewServer(s)
			defer host.Close()
			defer s.Close(context.Background())
			resp, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 201 {
				t.Fatal(resp.StatusCode, string(body))
			}
			id := obj(body).String("sessionId")
			ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			frames := make(chan string, 1024)
			go func() {
				defer close(frames)
				for {
					var data string
					if websocket.Message.Receive(ws, &data) != nil {
						return
					}
					frames <- data
				}
			}()
			if err = websocket.Message.Send(ws, `{"type":"user_message","content":"Launch the background shell and report its task result"}`); err != nil {
				t.Fatal(err)
			}
			select {
			case <-rootResults:
			case <-time.After(5 * time.Second):
				t.Fatal("native root result absent")
			}
			pidBytes, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal("actual Bash didn't create its PID file", err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if err != nil || pid <= 0 {
				t.Fatal(pid, err)
			}
			defer syscall.Kill(pid, syscall.SIGKILL)
			if err = syscall.Kill(pid, 0); err != nil {
				t.Fatal("owned background process already gone", err)
			}
			if action == "disconnect-deadline" {
				ws.Close()
				var deadline *desktopDeadlineTimer
				select {
				case deadline = <-deadlines:
				case <-time.After(3 * time.Second):
					t.Fatal("disconnected native Bash didn't arm its ceiling")
				}
				done := make(chan struct{})
				go func() { deadline.callback(); close(done) }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("native deadline didn't finish cancel/join")
				}
			} else if action == "desktop-shutdown" {
				if err = s.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = websocket.Message.Send(ws, `{"type":"stop_generation"}`); err != nil {
					t.Fatal(err)
				}
			}
			end := time.Now().Add(3 * time.Second)
			for {
				err = syscall.Kill(pid, 0)
				if errors.Is(err, syscall.ESRCH) {
					break
				}
				if time.Now().After(end) {
					t.Fatalf("actual Bash PID %d survived %s: %v", pid, action, err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if action == "stop-and-continue" {
				if err = websocket.Message.Send(ws, `{"type":"user_message","content":"Continue this same session after Stop"}`); err != nil {
					t.Fatal(err)
				}
				deadline := time.After(5 * time.Second)
				found := false
				for !found {
					select {
					case data, ok := <-frames:
						if !ok {
							t.Fatal("socket closed before same-session continuation")
						}
						found = strings.Contains(data, "NATIVE_AFTER_STOP_DONE")
					case <-deadline:
						t.Fatal("same-session continuation timed out")
					}
				}

				if !found {
					t.Fatal("same native session did not continue after Stop")
				}
			}
			// Read the renderer-facing history API after the real task group joined.
			historyURL := host.URL
			if action == "desktop-shutdown" {
				cold, coldErr := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: factory})
				if coldErr != nil {
					t.Fatal(coldErr)
				}
				defer cold.Close(context.Background())
				coldHost := httptest.NewServer(cold)
				defer coldHost.Close()
				historyURL = coldHost.URL
			}
			end = time.Now().Add(3 * time.Second)
			for {
				resp, err = host.Client().Get(historyURL + "/api/sessions/" + id + "/messages")
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				expectedStatus := "stopped"
				if action == "disconnect-deadline" {
					expectedStatus = "failed"
				}
				if bytes.Contains(body, []byte(`"status":"`+expectedStatus+`"`)) {
					break
				}
				if time.Now().After(end) {
					t.Fatal("stopped task was absent from canonical history API", string(body))
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
