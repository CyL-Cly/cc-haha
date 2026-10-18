package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSDKPromptSuggestionInputInterruptEndAndEOFJoinHTTP(t *testing.T) {
	for _, action := range []string{"next-input", "interrupt", "end_session", "eof"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			entered, canceled := make(chan struct{}), make(chan struct{})
			var rootCalls, suggestionCalls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if !bytes.Contains(body, []byte("[SUGGESTION MODE:")) {
					round := rootCalls.Add(1)
					emitPlanFixture(w, int(round), "", fmt.Sprintf("PARENT_RESULT_%d", round))
					return
				}
				if suggestionCalls.Add(1) == 1 {
					close(entered)
					select {
					case <-r.Context().Done():
						close(canceled)
					case <-ctx.Done():
					}
					return
				}
				emitPlanFixture(w, 3, "", "done") // Actual filter suppresses a later fork.
			}))
			defer func() { cancel(); peer.Close() }()
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			for key, value := range map[string]string{"ANTHROPIC_BASE_URL": peer.URL, "ANTHROPIC_API_KEY": "fake-suggestion-cancel", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
				f.n.Environment[key] = value
			}
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			results, ack := make(chan struct{}, 4), make(chan struct{}, 4)
			var mu sync.Mutex
			var frames []protocol.JSONObject
			var diagnostics bytes.Buffer
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private suggestion cancellation", MaxTurns: 6, Tools: []string{"Write"}, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
				var frame protocol.JSONObject
				if err := frame.UnmarshalJSON(data); err != nil {
					return err
				}
				mu.Lock()
				frames = append(frames, frame)
				mu.Unlock()
				if frame.String("type") == "result" {
					results <- struct{}{}
				}
				if frame.String("type") == "control_response" {
					response, _ := frame.Object("response")
					if response.String("request_id") == "cancel-action" {
						ack <- struct{}{}
					}
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			inputDone := make(chan error, 1)
			var eofAt time.Time
			go func() {
				write := func(value any) error { _, err := fmt.Fprintln(writer, string(rawMain(value))); return err }
				wait := func(signal <-chan struct{}) error {
					select {
					case <-signal:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if err := write(map[string]any{"type": "control_request", "request_id": "init", "request": map[string]any{"subtype": "initialize", "promptSuggestions": true}}); err != nil {
					inputDone <- err
					return
				}
				for _, text := range []string{"PARENT_FIRST", "PARENT_SECOND"} {
					if err := write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}); err != nil {
						inputDone <- err
						return
					}
					if err := wait(results); err != nil {
						inputDone <- err
						return
					}
				}
				if err := wait(entered); err != nil {
					inputDone <- err
					return
				}
				if action == "next-input" {
					if err := write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "PARENT_AFTER_CANCEL"}}); err != nil {
						inputDone <- err
						return
					}
					if err := wait(results); err != nil {
						inputDone <- err
						return
					}
				} else if action != "eof" {
					if err := write(map[string]any{"type": "control_request", "request_id": "cancel-action", "request": map[string]any{"subtype": action}}); err != nil {
						inputDone <- err
						return
					}
					if err := wait(ack); err != nil {
						inputDone <- err
						return
					}
				}
				eofAt = time.Now()
				inputDone <- writer.Close()
			}()
			if _, err = m.Run(ctx); err != nil {
				t.Fatal("actual cancellation SDK input", err, diagnostics.String())
			}
			if err = <-inputDone; err != nil {
				t.Fatal(err)
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal("physical join", err)
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("closed physical client left its held HTTP handler valid", ctx.Err())
			}
			mu.Lock()
			output := append([]protocol.JSONObject{}, frames...)
			mu.Unlock()
			for _, frame := range output {
				if frame.String("type") == "prompt_suggestion" {
					t.Fatal("cancelled or filtered fork published", string(rawMain(frame)))
				}
			}
			want := int32(2)
			if action == "next-input" {
				want = 3
			}
			if rootCalls.Load() != want {
				t.Fatal("cancel crossed into parent query", rootCalls.Load(), want)
			}
			if action == "eof" && time.Since(eofAt) < 4*time.Second {
				t.Fatal("EOF aborted before the Source 5-second suggestion drain")
			}
		})
	}
}
