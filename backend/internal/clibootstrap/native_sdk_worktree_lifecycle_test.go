package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldSDKWorktreeWriter struct {
	mu        sync.Mutex
	frames    []protocol.JSONObject
	requests  chan protocol.JSONObject
	cancelled chan struct{}
	once      sync.Once
}

func (w *heldSDKWorktreeWriter) Write(data []byte) (int, error) {
	var event protocol.JSONObject
	if e := event.UnmarshalJSON(bytes.TrimSpace(data)); e != nil {
		return 0, e
	}
	w.mu.Lock()
	w.frames = append(w.frames, event.Clone())
	w.mu.Unlock()
	if event.String("type") == "control_request" {
		w.requests <- event
	}
	if event.String("type") == "control_cancel_request" {
		w.once.Do(func() { close(w.cancelled) })
	}
	return len(data), nil
}
func TestActualPublicSDKWorktreeCallbackPendingCancelEOFAndOwnerClose(t *testing.T) {
	for _, action := range []string{"cancel", "EOF", "OwnerClose", "callback-timeout"} {
		t.Run(action, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, f.config+"/settings.json", `{"agentTeamsEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake629"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			var mu sync.Mutex
			count := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				mu.Lock()
				count++
				n := count
				mu.Unlock()
				if n == 1 {
					emitWorktreeModel(w, n, "Agent", `{"prompt":"Read hook-owned.txt","description":"held SDK hook","subagent_type":"general-purpose","isolation":"worktree"}`, "")
				} else if n == 2 && (action == "EOF" || action == "callback-timeout") {
					emitWorktreeModel(w, n, "", "", "actual hook failure observed")
				} else {
					t.Errorf("new model request after terminal callback action %s: %d", action, n)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			output := &heldSDKWorktreeWriter{requests: make(chan protocol.JSONObject, 2), cancelled: make(chan struct{})}
			var diagnostics bytes.Buffer
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics})
			if e != nil {
				t.Fatal(e)
			}
			lifetime, finish := context.WithTimeout(context.Background(), 10*time.Second)
			defer finish()
			turn, cancel := context.WithCancel(lifetime)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, e := m.Run(turn); done <- e }()
			timeout := ""
			if action == "callback-timeout" {
				timeout = `,"timeout":0.003`
			}
			go func() {
				_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"init629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["hold629"]` + timeout + `}]}}}` + "\n" + `{"type":"user","message":{"role":"user","content":"Create an isolated Agent through the actual SDK callback"}}` + "\n"))
			}()
			var frame protocol.JSONObject
			select {
			case frame = <-output.requests:
			case <-lifetime.Done():
				t.Fatal("real callback request not reached", diagnostics.String())
			}
			request, _ := frame.Object("request")
			if request.String("subtype") != "hook_callback" {
				t.Fatal("actual callback transport frame", rawMain(frame))
			}
			switch action {
			case "cancel":
				cancel()
				// The original Worktree wrapper supplies no model abort signal. Its
				// live peer response remains independently consumable after cancellation.
				response := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_response")}, protocol.JSONField{Name: "response", Value: rawMain(map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": f.cwd}}})})
				encoded, _ := protocol.EncodeNDJSON(response)
				go func() { _, _ = writer.Write(encoded); _ = writer.Close() }()

			case "EOF":
				_ = writer.Close()
			case "OwnerClose":
				m.Owner.Close()
			case "callback-timeout":
				select {
				case <-output.cancelled:
					_ = writer.Close()
				case <-lifetime.Done():
					t.Fatal("actual SDK callback timeout did not emit cancel")
				}
			}
			select {
			case e = <-done:
			case <-lifetime.Done():
				t.Fatal("actual callback action stranded main Owner", action)
			}
			if action == "OwnerClose" && !errors.Is(e, cooperative.ErrClosed) {
				t.Fatal("Owner Close lost terminal identity", e)
			}
			if action == "cancel" && e != nil && !errors.Is(e, context.Canceled) {
				t.Fatal("parent turn cancellation", e)
			}
			if (action == "EOF" || action == "callback-timeout") && e != nil {
				t.Fatal("original callback catch did not return empty output", e, diagnostics.String())
			}
			if action == "cancel" {
				select {
				case <-output.cancelled:
					t.Fatal("Worktree wrapper inherited parent abort unlike original Source")
				default:
				}
			}
			select {
			case <-m.Owner.Done():
			default:
				t.Fatal("public Run did not close its canonical Owner")
			}
			cleanupCtx, closeCleanup := context.WithTimeout(context.Background(), 3*time.Second)
			defer closeCleanup()
			e = m.ShutdownSession(cleanupCtx)
			if action == "OwnerClose" {
				if e != nil && !errors.Is(e, cooperative.ErrClosed) {
					t.Fatal("closed owner resource cleanup", e)
				}
			} else if e != nil && !errors.Is(e, cooperative.ErrClosed) {
				t.Fatal("physical Close", e)
			}
			mu.Lock()
			defer mu.Unlock()
			want := 1
			if action == "EOF" || action == "callback-timeout" {
				want = 2
			}
			if count != want {
				t.Fatal("terminal model request cardinality", count, want)
			}
			if action == "EOF" || action == "callback-timeout" {
				if !strings.Contains(diagnostics.String(), "Error in hook callback hold629:") {
					t.Fatal("original SDK callback diagnostic missing", diagnostics.String())
				}
			}
		})
	}
}
