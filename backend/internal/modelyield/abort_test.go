package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func abortEvent(raw string) string {
	var part struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &part); err != nil {
		panic(err)
	}
	return "event: " + part.Type + "\ndata: " + raw + "\n\n"
}

// These schedules exercise the production transport and the Source cancellation
// branches at claude.ts preservePartialText/flushWithoutToolUse. The response
// stops on a real open socket; there is no fake completed response or tool result.
func TestCallerAbortFlushesVisibleTextWithoutCommittingLocalTools(t *testing.T) {
	textStart := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ignored"}}`
	textDelta := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`
	textStop := `{"type":"content_block_stop","index":0}`
	for _, mode := range []string{"completed-text", "unfinished-text", "pending-local-tool", "server-boundary"} {
		t.Run(mode, func(t *testing.T) {
			parts := []string{textStart, textDelta}
			abortIndex, abortType := 0, "content_block_delta"
			expected := []string{"text"}
			idsWant := 1
			if mode != "unfinished-text" {
				parts = append(parts, textStop)
				abortType = "content_block_stop"
			}
			if mode == "pending-local-tool" {
				parts = append(parts, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"unsafe","id":"local","input":{}}}`, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`, `{"type":"content_block_stop","index":1}`)
				abortIndex = 1
				idsWant = 2
			}
			if mode == "server-boundary" {
				parts = append(parts, `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","name":"search","id":"server","input":{}}}`, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`, `{"type":"content_block_stop","index":1}`, `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"later visible"}}`)
				abortIndex = 2
				abortType = "content_block_delta"
				expected = []string{"text", "server_tool_use", "text"}
				idsWant = 3
			}
			prefix := responseStart
			for _, part := range parts {
				prefix += abortEvent(part)
			}
			var requests atomic.Int32
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "abort-physical")
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "offline-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ids, clocks := 0, 0
			envelopes := []*streamhistory.Envelope{}
			result, err := Run(ctx, input, Dependencies{Request: requestretry.Dependencies{UUID: func() string { ids++; return fmt.Sprintf("abort-id-%d", ids) }, Now: func() time.Time { clocks++; return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
				if value.Notice != nil {
					t.Error("cancelled response was retried")
				}
				if value.Update.Envelope != nil {
					envelopes = append(envelopes, value.Update.Envelope)
				} else {
					var part struct {
						Type  string `json:"type"`
						Index int    `json:"index"`
					}
					if err := json.Unmarshal(value.Update.Event, &part); err != nil {
						return 0, err
					}
					if part.Type == abortType && part.Index == abortIndex {
						cancel()
					}
				}
				return requestretry.Resume, nil
			}})
			if err != nil || result.Reason != "caller_aborted" || result.Terminal {
				t.Fatalf("abort outcome=%+v error=%v", result, err)
			}
			if len(envelopes) != len(expected) {
				t.Fatalf("visible commit count=%d want=%d", len(envelopes), len(expected))
			}
			for index, envelope := range envelopes {
				if envelope.APIError || envelope.Content[0].Object.String("type") != expected[index] {
					t.Fatalf("unsafe/synthetic commit: %+v", envelope)
				}
				raw, err := envelope.MarshalJSON()
				if err != nil || strings.Contains(string(raw), `"stop_reason":"end_turn"`) {
					t.Fatalf("cancelled response invented completion: %s %v", raw, err)
				}
			}
			if ids != idsWant || clocks != idsWant || requests.Load() != 1 {
				t.Fatalf("allocations/replay changed: ids=%d dates=%d HTTP=%d", ids, clocks, requests.Load())
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("aborted physical connection remained open")
			}
		})
	}
}

func TestAbortFlushConsumerRetainsStopAndErrorOwnership(t *testing.T) {
	for _, mode := range []string{"stop", "error", "wrapped-stop-error", "invalid-action"} {
		t.Run(mode, func(t *testing.T) {
			prefix := responseStart + abortEvent(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + abortEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "offline-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := errors.New("caller owns partial flush failure")
			if mode == "wrapped-stop-error" {
				original = fmt.Errorf("caller wrapper: %w", requestretry.ErrConsumerStopped)
			}
			committed, ids := 0, 0
			result, err := Run(ctx, input, Dependencies{Request: requestretry.Dependencies{UUID: func() string { ids++; return fmt.Sprint(ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
				if value.Notice != nil {
					t.Fatal("cancelled partial was retried")
				}
				if value.Update.Envelope != nil {
					committed++
					if mode == "stop" {
						return requestretry.Stop, nil
					}
					if mode == "invalid-action" {
						return 0, nil
					}
					return 0, original
				}
				var part struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(value.Update.Event, &part); err != nil {
					return 0, err
				}
				if part.Type == "content_block_delta" {
					cancel()
				}
				return requestretry.Resume, nil
			}})
			switch mode {
			case "stop":
				if err != nil || result.Reason != "consumer_stopped" {
					t.Fatalf("explicit stop=%+v error=%v", result, err)
				}
			case "invalid-action":
				var failure *CapabilityError
				if !errors.As(err, &failure) || failure.Boundary != "consumer action" {
					t.Fatalf("invalid action=%v", err)
				}
			default:
				if err != original {
					t.Fatalf("caller error identity swallowed: %v", err)
				}
			}
			if committed != 1 || ids != 1 || requests.Load() != 1 {
				t.Fatalf("consumer boundary advanced: committed=%d IDs=%d HTTP=%d", committed, ids, requests.Load())
			}
		})
	}
}
