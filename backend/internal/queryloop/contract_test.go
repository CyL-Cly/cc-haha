package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// Goldens were recorded by actual production query(), productionDeps(), SDK
// HTTP and toolExecution in query-loop.test.ts. They retain original-history,
// raw envelope/yield bytes, live-reference updates, allocation and side effects.
func TestActualSourceSuccessfulToolRounds(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Scenario    fixtureInput
			Requests    []fixtureRequest
			Observation json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus.Cases) != 22 {
		t.Fatalf("actual Source corpus: %v %d", err, len(corpus.Cases))
	}
	for _, item := range corpus.Cases {
		t.Run(item.Scenario.Name, func(t *testing.T) {
			var request int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				index := request
				request++
				if index >= len(item.Requests) {
					t.Error("unexpected continuation")
					http.Error(w, "unexpected", 400)
					return
				}
				want := item.Requests[index]
				if r.Method != "POST" || r.URL.RequestURI() != want.Path || string(body) != want.Body {
					t.Errorf("independently built request changed: method=%s path=%s body=%s", r.Method, r.URL.RequestURI(), body)
				}
				for _, pair := range want.Headers {
					if r.Header.Get(pair[0]) != pair[1] {
						t.Errorf("resolved pre-fetch header %s changed: %s", pair[0], r.Header.Get(pair[0]))
					}
				}

				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("request-id", fmt.Sprintf("fixture-request-%d", request))
				_, _ = io.WriteString(w, item.Scenario.Wires[index])
			}))
			defer server.Close()
			input := item.Scenario
			input.Home = t.TempDir()
			input.Origin = server.URL

			actual, err := fixtureRun(input)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(item.Observation, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("actual Source query contract changed\nwant: %s\ngot: %s", item.Observation, encoded)
			}
			if request != 2 {
				t.Fatalf("requests=%d", request)
			}
		})
	}
}

func event(raw string) string {
	var part map[string]any
	_ = json.Unmarshal([]byte(raw), &part)
	return "event: " + part["type"].(string) + "\ndata: " + raw + "\n\n"
}

const start = `{"type":"message_start","message":{"id":"model","type":"message","role":"assistant","model":"fixture","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`
const textStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
const textDelta = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`
const blockStop = `{"type":"content_block_stop","index":0}`
const terminal = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`
const stop = `{"type":"message_stop"}`

func completeText() string {
	return event(start) + event(textStart) + event(textDelta) + event(blockStop) + event(terminal) + event(stop)
}
func completeTool() string {
	return event(start) + event(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"write","id":"call","input":{}}}`) + event(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"original\":1}"}}`) + event(blockStop) + event(terminal) + event(stop)
}

type harness struct {
	Options  Options
	Host     Host
	History  []Message
	Emit     Emit
	Yielded  []Yield
	Order    []string
	UUIDs    int
	Requests *atomic.Int32
}

func testHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	h := &harness{Requests: &atomic.Int32{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.Requests.Add(1); handler(w, r) }))
	t.Cleanup(server.Close)
	h.Options = Options{Profile: SuccessfulToolRound, Model: "fixture-model", QuerySource: "sdk", NonInteractive: true, MaxTurns: 2, UUID: func() string { h.UUIDs++; return fmt.Sprint(h.UUIDs) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, ToolContext: "initial"}
	h.History = []Message{{Record: fixtureObject(`{"type":"user","message":{"role":"user","content":"begin"}}`)}}
	h.Emit = func(yield Yield) error {
		h.Yielded = append(h.Yielded, yield)
		if yield.RequestStart {
			h.Order = append(h.Order, "request")
		} else if yield.Message != nil {
			h.Order = append(h.Order, "message:"+yield.Message.Type())
		} else {
			h.Order = append(h.Order, "event")
		}
		return nil
	}
	h.Host = Host{
		Preflight: func(_ context.Context, round Round) ([]Message, error) {
			h.Order = append(h.Order, "preflight")
			return round.History, nil
		},
		ResolveModelInput: func(_ context.Context, round Round) (requestretry.Input, error) {
			h.Order = append(h.Order, "prepare")
			values, err := Snapshot(round.History)
			if err != nil {
				return requestretry.Input{}, err
			}
			original := []protocol.JSONObject{}
			for _, raw := range values {
				var item protocol.JSONObject
				if err := item.UnmarshalJSON(raw); err != nil {
					return requestretry.Input{}, err
				}
				original = append(original, item)
			}
			return requestretry.Input{Context: requestcontext.Context{History: original, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: h.Options.Model, QuerySource: h.Options.QuerySource, IsNonInteractiveSession: h.Options.NonInteractive}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "private-unit", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0"}, DeviceID: "unit-device", SessionID: "unit-session"}}, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}}, nil
		},
		Request:        requestretry.Dependencies{Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }},
		LookupBackfill: func(string) (Backfill, error) { return nil, nil },
		ExecuteBatch: func(_ context.Context, round Round, _ []Invocation, emit func(Message) error) (any, error) {
			h.Order = append(h.Order, "tools")
			err := emit(Message{Record: fixtureObject(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"ok"}]}}`)})
			return "updated", err
		},
		NormalizeToolResult: func(message Message) ([]Message, error) {
			h.Order = append(h.Order, "normalize")
			return []Message{message}, nil
		},
		PostSampling: func(Round, []*streamhistory.Envelope) error { h.Order = append(h.Order, "post"); return nil },
		StopFailure:  func(Round, *streamhistory.Envelope) error { h.Order = append(h.Order, "stop-failure"); return nil },
		AbortCleanup: func(context.Context, Round) error { h.Order = append(h.Order, "abort-cleanup"); return nil },
		StopHooks: func(context.Context, Round, []*streamhistory.Envelope, Emit) (StopDecision, error) {
			h.Order = append(h.Order, "stop")
			return StopDecision{}, nil
		},
		PauseAfterTools: func(context.Context, Round) (bool, error) { h.Order = append(h.Order, "pause"); return false, nil },
	}
	return h
}
func wireHandler(wire string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, wire)
	}
}
func (h *harness) run(ctx context.Context) (Outcome, error) {
	return Run(ctx, h.History, h.Options, h.Host, h.Emit)
}
func scope(t *testing.T, err error, boundary string) {
	t.Helper()
	var failure *ScopeError
	if !errors.Is(err, ErrUnsupported) || !errors.As(err, &failure) || failure.Boundary != boundary {
		t.Fatalf("scope=%s error=%v", boundary, err)
	}
}

func TestNoImplicitHostCapabilitiesOrExecutionProfile(t *testing.T) {
	h := testHarness(t, wireHandler(completeText()))
	for _, modify := range []func(){func() { h.Options.Profile = "" }, func() { h.Options.StreamingToolExecution = true }} {
		options := h.Options
		modify()
		_, err := h.run(context.Background())
		scope(t, err, "resolved execution profile")
		h.Options = options
	}
	options := h.Options
	for _, modify := range []func(){func() { h.Options.Model = "" }, func() { h.Options.QuerySource = "" }, func() { h.Options.MaxTurns = 0 }, func() { h.Options.UUID = nil }, func() { h.Options.Now = nil }} {
		modify()
		if _, err := h.run(context.Background()); err == nil {
			t.Fatal("implicit runtime option accepted")
		}
		h.Options = options
	}
	missingRequest := h.Host
	missingRequest.Request.Random = nil
	if _, err := Run(context.Background(), h.History, h.Options, missingRequest, h.Emit); err == nil {
		t.Fatal("missing original request host accepted")
	}
	if _, err := Run(context.Background(), h.History, h.Options, h.Host, nil); err == nil {
		t.Fatal("missing consumer accepted")
	}
	for _, modify := range []func(*Host){func(h *Host) { h.Preflight = nil }, func(h *Host) { h.ResolveModelInput = nil }, func(h *Host) { h.LookupBackfill = nil }, func(h *Host) { h.ExecuteBatch = nil }, func(h *Host) { h.NormalizeToolResult = nil }, func(h *Host) { h.PostSampling = nil }, func(h *Host) { h.StopHooks = nil }, func(h *Host) { h.StopFailure = nil }, func(h *Host) { h.AbortCleanup = nil }, func(h *Host) { h.PauseAfterTools = nil }} {
		host := h.Host
		modify(&host)
		if _, err := Run(context.Background(), h.History, h.Options, host, h.Emit); err == nil {
			t.Fatal("implicit host capability accepted")
		}
	}
	if h.Requests.Load() != 0 || h.UUIDs != 0 || len(h.Yielded) != 0 {
		t.Fatal("rejected profile consumed an ID, emitted or connected")
	}
}

func TestHostAndConsumerFailuresRetainOwnership(t *testing.T) {
	sentinel := errors.New("host/consumer stopped")
	for _, point := range []string{"request", "preflight", "prepare", "stream", "assistant-finish", "assistant-feed", "lookup", "backfill", "tools", "tool-yield", "normalize", "pause", "post", "stop"} {
		t.Run(point, func(t *testing.T) {
			wire := completeText()
			if point == "lookup" || point == "backfill" || point == "tools" || point == "tool-yield" || point == "normalize" || point == "pause" {
				wire = completeTool()
			}
			if point == "assistant-feed" {
				wire = event(start) + event(`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"server","name":"lookup"}}`) + event(blockStop) + event(terminal) + event(stop)
			}
			h := testHarness(t, wireHandler(wire))
			originalEmit := h.Emit
			h.Emit = func(yield Yield) error {
				if point == "request" && yield.RequestStart || point == "stream" && yield.Stream != nil || (point == "assistant-finish" || point == "assistant-feed") && yield.Message != nil || point == "tool-yield" && yield.Message != nil && yield.Message.Type() == "user" {
					return sentinel
				}
				return originalEmit(yield)
			}
			switch point {
			case "post":
				h.Host.PostSampling = func(Round, []*streamhistory.Envelope) error { return sentinel }
			case "preflight":
				h.Host.Preflight = func(context.Context, Round) ([]Message, error) { return nil, sentinel }
			case "prepare":
				h.Host.ResolveModelInput = func(context.Context, Round) (requestretry.Input, error) { return requestretry.Input{}, sentinel }
			case "lookup":
				h.Host.LookupBackfill = func(string) (Backfill, error) { return nil, sentinel }
			case "backfill":
				h.Host.LookupBackfill = func(string) (Backfill, error) { return func(*protocol.JSONObject) error { return sentinel }, nil }
			case "tools":
				h.Host.ExecuteBatch = func(context.Context, Round, []Invocation, func(Message) error) (any, error) { return nil, sentinel }
			case "normalize":
				h.Host.NormalizeToolResult = func(Message) ([]Message, error) { return nil, sentinel }
			case "pause":
				h.Host.PauseAfterTools = func(context.Context, Round) (bool, error) { return false, sentinel }
			case "stop":
				h.Host.StopHooks = func(context.Context, Round, []*streamhistory.Envelope, Emit) (StopDecision, error) {
					return StopDecision{}, sentinel
				}
			}
			_, err := h.run(context.Background())
			if err != sentinel || errors.Is(err, ErrUnsupported) {
				t.Fatalf("caller failure ownership changed: %v", err)
			}
			if h.Requests.Load() > 1 {
				t.Fatal("failed callback caused continuation/retry")
			}
			if point == "request" && h.UUIDs != 0 {
				t.Fatal("request consumer failure consumed chain ID")
			}
			if point == "tool-yield" && strings.Contains(strings.Join(h.Order, ","), "normalize") {
				t.Fatal("tool result normalized before its yield was accepted")
			}
		})
	}
}

func TestResolvedHooksAndToolContextPreserveSourceOrder(t *testing.T) {
	for _, decision := range []StopDecision{{PreventContinuation: true}, {PreventContinuation: true, BlockingMessages: []Message{{}}}, {BlockingMessages: []Message{{}}}} {
		t.Run(fmt.Sprint(decision.PreventContinuation, len(decision.BlockingMessages)), func(t *testing.T) {
			h := testHarness(t, wireHandler(completeText()))
			h.Host.StopHooks = func(context.Context, Round, []*streamhistory.Envelope, Emit) (StopDecision, error) {
				return decision, nil
			}
			outcome, err := h.run(context.Background())
			if decision.PreventContinuation {
				if err != nil || outcome.Reason != "stop_hook_prevented" {
					t.Fatalf("prevent must win before blocking: %+v %v", outcome, err)
				}
			} else {
				scope(t, err, "blocking stop-hook continuation")
			}
		})
	}
	for _, pause := range []bool{true, false} {
		t.Run(fmt.Sprint("pause=", pause), func(t *testing.T) {
			h := testHarness(t, wireHandler(completeTool()))
			h.Options.InitialTracking = &Tracking{ChainID: "existing", Depth: 3}
			h.Options.MaxTurns = 1
			h.Host.NormalizeToolResult = func(message Message) ([]Message, error) {
				h.Order = append(h.Order, "normalize")
				return []Message{{Record: fixtureObject(`{"type":"attachment"}`)}, message}, nil
			}
			h.Host.PauseAfterTools = func(_ context.Context, round Round) (bool, error) {
				h.Order = append(h.Order, "pause")
				if round.ToolContext != "updated" || round.Tracking.ChainID != "existing" || round.Tracking.Depth != 4 {
					t.Errorf("context/tracking=%+v", round)
				}
				return pause, nil
			}
			outcome, err := h.run(context.Background())
			if pause {
				if err != nil || outcome.ToolContext != "updated" || outcome.Reason != "completed" || len(outcome.History) != 3 || outcome.History[2].Type() != "user" {
					t.Fatalf("tool completion=%+v %v", outcome, err)
				}
			} else {
				if err != nil || outcome.Reason != "max_turns" || len(h.Yielded) == 0 || h.Yielded[len(h.Yielded)-1].Message.Type() != "attachment" {
					t.Fatalf("max-turn attachment after tools=%+v %v", outcome, err)
				}
			}
			want := []string{"post", "tools", "message:user", "normalize", "pause"}
			var relevant []string
			for _, part := range h.Order {
				for _, name := range want {
					if part == name {
						relevant = append(relevant, part)
					}
				}
			}
			if !reflect.DeepEqual(relevant, want) {
				t.Fatalf("side effect order=%v", relevant)
			}
		})
	}
}

func TestAttemptFeedbackAndTransportFailureAreExplicit(t *testing.T) {
	cases := []struct {
		name, wire, boundary string
		status               int
	}{
		{"HTTP429", "", "typed no-status SDK creation error", 429},
		{"SDKerror", "event: error\ndata: {\"error\":{\"message\":\"offline reset\"}}\n\n", "partial-stream error, retry and commit buffer", 200},
		{"badJSON", "event: message_start\ndata: {\n\n", "partial-stream error, retry and commit buffer", 200},
		{"badController", event(`{"type":"message_delta","delta":null}`), "partial-stream error, retry and commit buffer", 200},
		{"maxTokens", strings.Replace(completeText(), `"end_turn"`, `"max_tokens"`, 1), "output-limit recovery", 200},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			h := testHarness(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(item.status)
				_, _ = io.WriteString(w, item.wire)
			})
			_, err := h.run(context.Background())
			switch item.name {
			case "HTTP429":
				var failure *apipresentation.CapabilityError
				if !errors.As(err, &failure) || failure.Boundary != item.boundary {
					t.Fatalf("API capability drift: %v", err)
				}
			case "maxTokens":
				scope(t, err, item.boundary)
			default:
				var failure *modelyield.CapabilityError
				if !errors.As(err, &failure) || failure.Boundary != item.boundary {
					t.Fatalf("model capability drift: %v", err)
				}
			}
			if h.Requests.Load() != 1 {
				t.Fatal("unimplemented policy retried")
			}
			for _, part := range h.Order {
				if part == "tools" || part == "stop" {
					t.Fatal("failed attempt executed tools/hooks")
				}
			}
		})
	}
}

func TestCancellationAfterAcceptedEnvelopeNeverExecutesTools(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	originalEmit := h.Emit
	h.Emit = func(yield Yield) error {
		if yield.Message != nil {
			cancel()
		}
		return originalEmit(yield)
	}
	_, err := h.run(ctx)
	scope(t, err, "pending tool cancellation/results")
	for _, part := range h.Order {
		if part == "tools" || part == "stop" {
			t.Fatal("cancelled attempt executed tools/hooks")
		}
	}
}

func TestLiteralSubmitAbortAtRawEventDoesNotCommitPendingTools(t *testing.T) {
	h := testHarness(t, wireHandler(completeTool()))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	originalEmit := h.Emit
	h.Emit = func(yield Yield) error {
		if yield.Stream != nil {
			cancel(SubmitInterrupt)
		}
		return originalEmit(yield)
	}
	result, err := h.run(ctx)
	if err != nil || result.Reason != "aborted_streaming" || len(result.History) != 1 {
		t.Fatalf("literal submit abort=%+v error=%v", result, err)
	}
	// The SDK iterator may drain already-buffered raw events after abort and
	// Controller may allocate at block_stop. The unsafe transition is accepting
	// pending tool envelopes at EOF, then executing them as a completed turn.
	for _, yield := range h.Yielded {
		if yield.Message != nil {
			t.Fatal("aborted iterator committed a pending assistant envelope")
		}
	}
	for _, part := range h.Order {
		if part == "post" || part == "tools" || part == "stop" {
			t.Fatal("aborted iterator was processed as a successful turn")
		}
	}
}

func TestMessageSnapshotsRetainHistoryBytesAndRejectInvalidMutation(t *testing.T) {
	message := Message{Record: fixtureObject(`{"type":"user","message":{"content":"\ud800","future":9007199254740993},"\udfff":1}`)}
	actual, err := Snapshot([]Message{message})
	if err != nil || string(actual[0]) != `{"type":"user","message":{"content":"\ud800","future":9007199254740992},"\udfff":1}` {
		t.Fatalf("snapshot=%s %v", actual, err)
	}
	message.Record.Set("future", []byte(`{`))
	if _, err := Snapshot([]Message{message}); err == nil {
		t.Fatal("malformed mutable history accepted")
	}
}

// The same assembler preserves the visible partial text and independently
// creates the Source incomplete-response assistant. The outer query accepts
// both, enters post-sampling, then chooses the last Record marker's StopFailure.
func TestTruncatedTextUsesStopFailureInsteadOfOrdinaryStopHooks(t *testing.T) {
	h := testHarness(t, wireHandler(event(start)+event(textStart)+event(textDelta)))
	result, err := h.run(context.Background())
	if err != nil || result.Reason != "completed" || len(result.History) != 3 {
		t.Fatalf("truncated outcome=%+v error=%v", result, err)
	}
	last := result.History[2].Assistant
	if last == nil || string(last.Record.Get("isApiErrorMessage")) != "true" {
		t.Fatal("last original assistant lost failure marker")
	}
	if strings.Join(h.Order[len(h.Order)-2:], ",") != "post,stop-failure" || h.Requests.Load() != 1 {
		t.Fatalf("terminal hook ordering/replay changed: %v", h.Order)
	}
}

func TestConsumerStopPreservesOnlyResumedHistory(t *testing.T) {
	for _, point := range []string{"request", "raw", "assistant", "second-tool-result"} {
		t.Run(point, func(t *testing.T) {
			wire := completeText()
			if point == "second-tool-result" {
				wire = completeTool()
			}
			h := testHarness(t, wireHandler(wire))
			if point == "second-tool-result" {
				h.Host.ExecuteBatch = func(_ context.Context, _ Round, _ []Invocation, emit func(Message) error) (any, error) {
					for _, content := range []string{"first", "second"} {
						message := Message{Record: fixtureObject(fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":%q}]}}`, content))}
						if err := emit(message); err != nil {
							return nil, err
						}
					}
					return "updated", nil
				}
			}
			count := 0
			outcome, err := RunControlled(context.Background(), h.History, h.Options, h.Host, func(value Yield) (requestretry.NoticeAction, error) {
				stop := point == "request" && value.RequestStart || point == "raw" && value.Stream != nil || point == "assistant" && value.Message != nil && value.Message.Type() == "assistant"
				if value.Message != nil && value.Message.Type() == "user" {
					count++
					if point == "second-tool-result" && count == 2 {
						stop = true
					}
				}
				if stop {
					return requestretry.Stop, nil
				}
				return requestretry.Resume, h.Emit(value)
			})
			want := 1
			if point == "second-tool-result" {
				want = 3
			}
			if err != nil || outcome.Reason != "consumer_stopped" || len(outcome.History) != want {
				t.Fatalf("consumer stop discarded accepted history: %+v %v", outcome, err)
			}
			for _, order := range h.Order {
				if order == "stop" || order == "pause" {
					t.Fatalf("consumer stop advanced lifecycle: %v", h.Order)
				}
			}
			if point == "assistant" && strings.Contains(strings.Join(h.Order, ","), "post") {
				t.Fatal("unresumed assistant entered post sampling")
			}
			if point == "request" && (h.UUIDs != 0 || h.Requests.Load() != 0) {
				t.Fatal("request start stop allocated or sent")
			}
		})
	}
}
