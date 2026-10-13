package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeThinkingSummariesActualTUIRequestStreamTranscriptAndColdResume(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		for _, setting := range []string{"missing", "false", "true"} {
			t.Run(fmt.Sprintf("interactive_%t_setting_%s", interactive, setting), func(t *testing.T) {
				var calls atomic.Int32
				redacted := interactive && setting != "true"
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					calls.Add(1)
					if r.URL.Host != "api.anthropic.com" || r.URL.Scheme != "http" {
						t.Error("not the actual official request through isolated HTTP proxy", r.URL)
					}
					if strings.Contains(r.Header.Get("Anthropic-Beta"), requestcontext.ThinkingSummaryRedactionBeta) != redacted {
						t.Error("actual request did not consume interactive setting", r.Header.Get("Anthropic-Beta"), redacted)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					emit := func(kind string, data any) { _, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw(data)) }
					emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "private-thinking-message", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6", "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}})
					if redacted {
						emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "redacted_thinking", "data": "PRIVATE_ENCRYPTED_DATA"}})
					} else {
						emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
						emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "PRIVATE_ACTUAL_MODEL_THINKING"}})
						emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "private-fixture-signature"}})
					}
					emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
					emit("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "PRIVATE_ACTUAL_SUMMARY_TASK_DONE"}})
					emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
					emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}})
					emit("message_stop", map[string]any{"type": "message_stop"})
				}))
				defer proxy.Close()
				o := nativeSessionOptions(t, "http://api.anthropic.com")
				o.Environment["HTTP_PROXY"] = proxy.URL
				o.Environment["CI"] = "1"
				// The original local preload sets this exact startup default.
				o.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
				for _, name := range []string{"HTTP_PROXY", "CI", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"} {
					o.Native.Environment[name] = o.Environment[name]
				}
				o.Arguments.Model = "claude-sonnet-4-6"
				o.Arguments.Tools = []string{"Read"}
				o.Interactive = interactive
				config := o.Environment["CLAUDE_CONFIG_DIR"]
				if err := os.MkdirAll(config, 0700); err != nil {
					t.Fatal(err)
				}
				settings := `{}`
				if setting != "missing" {
					settings = `{"showThinkingSummaries":` + setting + `}`
				}
				if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(settings), 0600); err != nil {
					t.Fatal(err)
				}
				for _, cold := range []bool{false, true} {
					if cold {
						o.Arguments.Resume = o.Arguments.SessionID
					}
					s, err := NewSession(context.Background(), o)
					if err != nil {
						t.Fatal(err)
					}
					state := tui.NewState(nil, 100, 30)
					state.ActiveTurn = "actual-thinking-turn"
					events := 0
					err = s.Submit(context.Background(), tui.Input{Text: "Complete private thinking-summary task", TurnID: state.ActiveTurn}, func(event tui.Event) error {
						if event.Kind == tui.ThinkingDelta {
							events++
						}
						state.Apply(event)
						return nil
					})
					closeErr := s.Close(context.Background())
					if err != nil || closeErr != nil {
						t.Fatal(err, closeErr)
					}
					if events == 0 {
						t.Fatal("actual model thinking stream never reached UI consumer")
					}
					if strings.Contains(state.View("", false).Content, "PRIVATE_ACTUAL_MODEL_THINKING") {
						t.Fatal("thinking content escaped ordinary chat view")
					}
					state.Transcript = true
					view := state.View("", false).Content
					if redacted && !strings.Contains(view, "✻ Thinking…") || !redacted && !strings.Contains(view, "PRIVATE_ACTUAL_MODEL_THINKING") || strings.Contains(view, "PRIVATE_ENCRYPTED_DATA") {
						t.Fatal("actual transcript consumer did not preserve source summary/placeholder", view)
					}
				}
				if calls.Load() != 2 {
					t.Fatal("thinking startup/cold profile added extra requests", calls.Load())
				}
			})
		}
	}
}
