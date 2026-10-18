package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/coder/websocket"
)

// Build the wrapper through the same remote-create and Session factories as
// RunCommand. The local and startup Owners retain their real cached-account
// readers; no Manager or credential callback is injected into the wrapper.
func remoteOwnerLifecycleSession(t *testing.T, ctx context.Context, handler http.Handler) (*terminalRemoteSession, *cliRemoteStartup) {
	t.Helper()
	model := httptest.NewServer(handler)
	t.Cleanup(model.Close)
	proxy, ca := claudeAIAPIPrivateTLS(t, handler)
	o := remoteCLIOptions(t, model.URL, proxy, ca)
	// RunCommand resolves this before entering prepareCLIRemoteStartup.
	o.Stderr = io.Discard
	for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
		environment["USER_TYPE"] = "ant"
		environment["CLAUDE_INTERNAL_FC_OVERRIDES"] = `{"tengu_remote_backend":true}`
		environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	global, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings := obj(global)
	settings.Set("lastReleaseNotesSeen", raw(o.Version))
	if err = os.MkdirAll(filepath.Join(config, "cache"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "cache", "changelog.md"), []byte("## 0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command, _, err := ParseCommand([]string{"--remote", "Actual remote owner lifecycle", "--model", "sonnet", "--tools", "", "--thinking", "disabled", "--max-turns", "8"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	command, startup, finished, err := prepareCLIRemoteStartup(ctx, command, &o, strings.NewReader(""), io.Discard, nil)
	if err != nil || finished || startup == nil || startup.created == nil {
		t.Fatal("actual remote startup", finished, err)
	}
	t.Cleanup(startup.Close)
	o.Arguments, o.Interactive = command.Arguments, true
	local, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	local.process.AdoptCLIStartupFiles(startup.process)
	remote, err := newTerminalRemoteSession(ctx, local, startup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close(context.Background()) })
	if remote.process != local.process || remote.process.Owner != local.process.Owner || remote.manager == nil {
		t.Fatal("remote wrapper replaced the actual local process/Owner")
	}
	return remote, startup
}

func TestActualRemoteSessionOwnerCallbacksHeldPOSTCancelAndCloseJoin(t *testing.T) {
	for _, mode := range []string{"before-start", "held-upgrade", "held-post-with-callbacks"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var models, subscriptions, posts atomic.Int32
			upgrade, connected := make(chan struct{}), make(chan *websocket.Conn, 1)
			held, cancelled, socketClosed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			controls := make(chan protocol.JSONObject, 4)
			var closeUpgrade, closeSocket sync.Once
			api := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if request.URL.Path == "/v1/messages" {
					models.Add(1)
					t.Error("remote lifecycle entered a local ASK/title model", request.Method)
					http.Error(w, "no local inference", http.StatusBadRequest)
					return
				}
				if request.Header.Get("Authorization") != "Bearer owned-ccr-oauth" || request.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("actual cached remote headers", request.Header)
				}
				switch request.URL.Path {
				case "/v1/environment_providers":
					_, _ = io.WriteString(w, `{"environments":[{"environment_id":"owned-cloud","kind":"anthropic_cloud"}]}`)
				case "/v1/sessions":
					data, _ := io.ReadAll(request.Body)
					if request.Method != http.MethodPost || !strings.Contains(string(data), "Actual remote owner lifecycle") {
						t.Error("actual remote create body", request.Method, string(data))
					}
					_, _ = io.WriteString(w, `{"id":"session_owned_lifecycle","title":"Actual remote owner lifecycle"}`)
				case "/v1/sessions/ws/session_owned_lifecycle/subscribe":
					subscriptions.Add(1)
					if request.URL.Query().Get("organization_uuid") != "owned-ccr-org" {
						t.Error("actual subscription organization", request.URL)
					}
					closeUpgrade.Do(func() { close(upgrade) })
					if mode == "held-upgrade" {
						<-request.Context().Done()
						closeSocket.Do(func() { close(socketClosed) })
						return
					}
					connection, err := websocket.Accept(w, request, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer connection.CloseNow()
					defer closeSocket.Do(func() { close(socketClosed) })
					connected <- connection
					for {
						_, data, err := connection.Read(request.Context())
						if err != nil {
							return
						}
						controls <- obj(data)
					}
				case "/v1/sessions/session_owned_lifecycle/events":
					data, err := io.ReadAll(request.Body)
					if err != nil || request.Method != http.MethodPost || !strings.Contains(string(data), "HELD_REMOTE_OWNER_POST") || request.Header.Get("x-organization-uuid") != "owned-ccr-org" || request.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" {
						t.Error("actual held POST body/headers", err, request.Method, string(data), request.Header)
					}
					if posts.Add(1) != 1 {
						t.Error("duplicate remote HTTP input")
					}
					close(held)
					// Consume the body first: otherwise server EOF observation can
					// wait for unread POST bytes and falsely diagnose client close.
					<-request.Context().Done()
					close(cancelled)
				default:
					t.Error("unowned remote lifecycle request", request.Method, request.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			remote, _ := remoteOwnerLifecycleSession(t, ctx, api)
			events := make(chan tui.Event, 64)
			remove := remote.SubscribeEnvironmentHookNotices(func(event tui.Event) {
				select {
				case events <- event:
				default:
					t.Error("unexpected remote event overflow")
				}
			})
			defer remove()
			var submitted chan error
			if mode != "before-start" {
				if err := remote.StartTerminalRemote(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case <-upgrade:
				case <-ctx.Done():
					t.Fatal("actual WS upgrade did not start")
				}
			}
			if mode == "held-post-with-callbacks" {
				var connection *websocket.Conn
				select {
				case connection = <-connected:
				case <-ctx.Done():
					t.Fatal("actual WS did not connect")
				}
				submitted = make(chan error, 1)
				go func() {
					submitted <- remote.Submit(ctx, tui.Input{Text: "HELD_REMOTE_OWNER_POST", Mode: "prompt"}, nil)
				}()
				select {
				case <-held:
				case err := <-submitted:
					t.Fatal("remote Submit returned before actual held POST", err)
				case <-ctx.Done():
					t.Fatal("actual remote HTTP did not enter")
				}
				if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"system","subtype":"task_started","task_id":"actual-remote-callback-task"}`)); err != nil {
					t.Fatal(err)
				}
				for {
					select {
					case event := <-events:
						if event.Kind == tui.TerminalRemoteStateChanged && event.RemoteState != nil && event.RemoteState.BackgroundTasks == 1 {
							goto callbackReceived
						}
					case <-ctx.Done():
						t.Fatal("WS callback could not enter the actual Owner while HTTP remained held")
					}
				}
			callbackReceived:
				remote.Cancel()
				select {
				case control := <-controls:
					request, _ := control.Object("request")
					if control.String("type") != "control_request" || request.String("subtype") != "interrupt" {
						t.Fatal("actual remote Cancel did not send interrupt", control)
					}
				case <-ctx.Done():
					t.Fatal("actual remote interrupt absent")
				}
				if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"control_request","request_id":"late-before-close","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"actual-remote-pending","input":{"command":"never-run-locally"}}}`)); err != nil {
					t.Fatal(err)
				}
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer closeCancel()
			closed := make(chan error, 1)
			go func() { closed <- remote.CloseWithReason(closeCtx, "prompt_input_exit") }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal("actual remote Close", err)
				}
			case <-closeCtx.Done():
				cancel()
				<-closed
				t.Fatal("remote Close joined a callback/transport before retiring its Owner admission")
			}
			if mode != "before-start" {
				select {
				case <-socketClosed:
				case <-ctx.Done():
					t.Fatal("actual held upgrade/WS was not joined")
				}
			}
			if submitted != nil {
				select {
				case err := <-submitted:
					if !errors.Is(err, cooperative.ErrClosed) && !errors.Is(err, context.Canceled) {
						t.Fatal("closed actual Submit outcome", err)
					}
				case <-ctx.Done():
					t.Fatal("remote Close retained actual Submit")
				}
				select {
				case <-cancelled:
				case <-ctx.Done():
					t.Fatal("actual HTTP peer did not observe close cancellation")
				}
			}
			if err := remote.Submit(ctx, tui.Input{Text: "LATE_REMOTE_INPUT"}, nil); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("closed wrapper accepted a new remote input", err)
			}
			if models.Load() != 0 || posts.Load() != map[bool]int32{true: 1}[submitted != nil] || subscriptions.Load() != map[bool]int32{true: 1}[mode != "before-start"] {
				t.Fatal("remote lifecycle leaked or duplicated work", models.Load(), posts.Load(), subscriptions.Load())
			}
			t.Log(fmt.Sprintf("actual remote lifecycle mode=%s HTTP=%d WS=%d localASK=%d close<3s", mode, posts.Load(), subscriptions.Load(), models.Load()))
		})
	}
}
