package tracecapture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type fixtureClone struct {
	chunks  [][]byte
	hang    bool
	failure error
	done    chan struct{}
	once    sync.Once
}

func (c *fixtureClone) Read(p []byte) (int, error) {
	if len(c.chunks) > 0 {
		b := c.chunks[0]
		n := copy(p, b)
		if n == len(b) {
			c.chunks = c.chunks[1:]
		} else {
			c.chunks[0] = b[n:]
		}
		return n, nil
	}
	if c.hang {
		<-c.done
		return 0, io.EOF
	}
	if c.failure != nil {
		return 0, c.failure
	}
	return 0, io.EOF
}
func (c *fixtureClone) Close() error { c.once.Do(func() { close(c.done) }); return nil }
func TestResponseCaptureAgainstActualSource(t *testing.T) {
	cases := []protocol.JSONObject{obj("chunks", []any{""}), obj("chunks", []any{"{\"usage\":{\"input_tokens\":3,\"output_tokens\":4},\"secret\":\"fake\"}"}, "headers", obj("content-type", "application/json")), obj("chunks", []any{[]int{0xef, 0xbb}, []int{0xbf, 0xf0, 0x9f}, []int{0x98, 0x80, 0xff, 0xff}}), obj("chunks", []any{"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n", "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "TRAIL_AFTER_STOP"}, "headers", obj("content-type", "text/event-stream")), obj("chunks", []any{"event: response.", "completed\ndata: {\"type\":\"response.completed\"}\n\n"}, "headers", obj("content-type", "text/event-stream"), "hang", true), obj("chunks", []any{"data: {\"type\":\"response.failed\"}\n\n"}, "headers", obj("content-type", "text/event-stream"), "hang", true), obj("chunks", []any{string(bytes.Repeat([]byte("a"), StreamCaptureBytes))}), obj("chunks", []any{string(bytes.Repeat([]byte("a"), StreamCaptureBytes+7))}), obj("chunks", []any{"partial body"}, "abort", true, "hang", true), obj("chunks", []any{}, "error", "read fixture failure"), obj("chunks", []any{"queued body"}, "alreadyAbort", true), obj("chunks", []any{[]int{0xef, 0xbb, 0xbf, 0x41}})}
	expected := array(source(t, obj("action", "response", "cases", cases)).Get("observations"))
	for n, row := range cases {
		t.Run(integerString(int64(n)), func(t *testing.T) {
			chunks := [][]byte{}
			for _, v := range array(row.Get("chunks")) {
				if len(v) > 0 && v[0] == '"' {
					chunks = append(chunks, []byte(text(v)))
				} else {
					a := array(v)
					b := make([]byte, len(a))
					for n, x := range a {
						b[n] = byte(number(x))
					}
					chunks = append(chunks, b)
				}
			}
			clone := &fixtureClone{chunks: chunks, done: make(chan struct{}), hang: truthy(row.Get("hang"))}
			if row.Get("error") != nil {
				clone.chunks = nil
				clone.failure = errors.New(text(row.Get("error")))
			}
			headers := http.Header{}
			for _, p := range object(row.Get("headers")).OwnProperties() {
				headers.Set(text(p.NameJSON), text(p.Value))
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if truthy(row.Get("alreadyAbort")) {
				cancel(errors.New("fixture abort"))
			}
			if truthy(row.Get("abort")) {
				time.AfterFunc(10*time.Millisecond, func() { cancel(errors.New("fixture abort")) })
			}
			capture, e := CaptureResponse(ctx, headers, clone, 25*time.Millisecond)
			out := obj()
			if e != nil {
				out.Set("error", raw(e.Error()))
			} else {
				value := obj("snapshot", capture.Snapshot, "aborted", capture.Aborted)
				if capture.AbortReason != nil {
					value.Set("abortReason", raw(capture.AbortReason.Error()))
				}
				out.Set("value", raw(value))
			}
			want := object(expected[n])
			want.Delete("cancelled")
			compare(t, want, out)
		})
	}
}
func TestRealCaptureBinderLWWAndErrorStates(t *testing.T) {
	ctx := context.Background()
	s, e := New(Options{Scope: fixtureHome(t), EntryPoint: "claude-desktop"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(ctx)
	binding := s.Bind(Binding{SessionID: "real-fetch", QuerySource: "repl_main_thread", Source: "anthropic", Provider: &ProviderInfo{ID: ptr("fake-provider-id"), Name: "Fixture", Format: "anthropic"}})
	req := FetchInput{Context: ctx, Method: "POST", URL: "https://example.invalid/messages", Headers: http.Header{"Authorization": {"Bearer fake-token"}}, PlainBody: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"run"}],"max_tokens":10}`)}
	capture := binding(req)
	if capture == nil {
		t.Fatal("Desktop actual POST capture disabled")
	}
	response := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\nTRAIL_AFTER_SDK_STOP"
	capture.HeadersResponse(200, http.Header{"Content-Type": {"text/event-stream"}}, io.NopCloser(strings.NewReader(response)))
	if e = s.Drain(ctx); e != nil {
		t.Fatal(e)
	}
	full, e := s.Full(ctx, "real-fetch")
	if e != nil {
		t.Fatal(e)
	}
	calls := array(full.Get("calls"))
	events := array(full.Get("events"))
	if len(calls) != 1 || len(events) != 2 || text(object(calls[0]).Get("status")) != "ok" {
		t.Fatalf("LWW capture missing: %s", raw(full))
	}
	c := object(calls[0])
	preview := text(object(object(c.Get("response")).Get("body")).Get("preview"))
	if !strings.HasSuffix(preview, "TRAIL_AFTER_SDK_STOP") {
		t.Fatalf("Clone prematurely stopped at SDK message_stop: %s", preview)
	}
	usage := object(c.Get("usage"))
	if number(usage.Get("inputTokens")) != 2 || number(usage.Get("outputTokens")) != 3 {
		t.Fatalf("Actual stream usage lost: %s", raw(usage))
	}
	if bytes.Contains(raw(c), []byte("fake-token")) {
		t.Fatal("Credentials persisted")
	}
	capture = binding(req)
	capture.FetchFailure(errors.New("Bearer failureFake secret"))
	capture.HeadersResponse(200, http.Header{}, io.NopCloser(strings.NewReader("duplicate")))
	if e = s.Drain(ctx); e != nil {
		t.Fatal(e)
	}
	full, e = s.Full(ctx, "real-fetch")
	if e != nil {
		t.Fatal(e)
	}
	if number(object(full.Get("summary")).Get("failedCalls")) != 1 || number(object(full.Get("summary")).Get("apiCalls")) != 2 {
		t.Fatalf("Actual failure/duplicate outcome broken: %s", raw(full))
	}
	capture = binding(req)
	capture.HeadersResponse(200, http.Header{}, nil)
	if e = s.Drain(ctx); e != nil {
		t.Fatal(e)
	}
	full, e = s.Full(ctx, "real-fetch")
	if e != nil {
		t.Fatal(e)
	}
	calls = array(full.Get("calls"))
	if text(object(calls[len(calls)-1]).Get("status")) != "error" || text(object(object(calls[len(calls)-1]).Get("metadata")).Get("phase")) != "response_capture_failed" {
		t.Fatal("Missing independent clone reported completion")
	}
}
func TestRequestCaptureEnablementAndInvalidIDs(t *testing.T) {
	env := map[string]string{}
	s, e := New(Options{Scope: fixtureHome(t), Env: func(k string) string { return env[k] }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if s.ShouldCapture() {
		t.Fatal("Plain CLI captured without opt-in")
	}
	env["CC_HAHA_TRACE_API_CALLS"] = " true "
	if !s.ShouldCapture() {
		t.Fatal("Source trimmed environment override")
	}
	env["CC_HAHA_TRACE_API_CALLS"] = " off "
	if s.Enabled() {
		t.Fatal("Source false environment override")
	}
	env["CC_HAHA_TRACE_API_CALLS"] = ""
	env["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
	if !s.ShouldCapture() {
		t.Fatal("Desktop default capture off")
	}
	disabled := false
	_, e = s.UpdateSettings(context.Background(), &disabled)
	if e != nil {
		t.Fatal(e)
	}
	if s.ShouldCapture() {
		t.Fatal("Setting disable ignored")
	}
	env["CC_HAHA_TRACE_API_CALLS"] = "on"
	if !s.ShouldCapture() {
		t.Fatal("Explicit env override ignored")
	}
	input := fixtureCall("a", "model", "ok")
	input.Set("sessionId", jstr(strings.Repeat("a", 513)))
	out, e := s.RecordCall(context.Background(), input)
	if e != nil || out.IsObject() {
		t.Fatal("Oversized session ID recorded")
	}
	if _, e = os.Stat(s.storageDir()); !os.IsNotExist(e) {
		t.Fatal("Invalid session ID wrote canonical files")
	}
}
func TestProcessCaptureGateAgainstSourceAndSameProducer(t *testing.T) {
	cases := []protocol.JSONObject{}
	for _, settingsEnabled := range []bool{true, false} {
		for _, entry := range []string{"sdk-cli", "claude-desktop", "cli", ""} {
			for _, flag := range []string{"", "1", "0", " false ", "\ufeffTRUE\ufeff", "invalid"} {
				cases = append(cases, obj("settingsEnabled", settingsEnabled, "environment", obj("CLAUDE_CODE_ENTRYPOINT", entry, "CC_HAHA_TRACE_API_CALLS", flag)))
			}
		}
	}
	expected := array(source(t, obj("action", "processGate", "cases", cases)).Get("observations"))
	ctx := context.Background()
	s, e := New(Options{Scope: fixtureHome(t), EntryPoint: "claude-desktop", Env: func(k string) string {
		if k == "CC_HAHA_TRACE_API_CALLS" {
			return "1"
		}
		return "claude-desktop"
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(ctx)
	for n, row := range cases {
		settingsEnabled := truthy(row.Get("settingsEnabled"))
		_, e = s.UpdateSettings(ctx, &settingsEnabled)
		if e != nil {
			t.Fatal(e)
		}
		env := map[string]string{}
		for _, p := range object(row.Get("environment")).OwnProperties() {
			env[text(p.NameJSON)] = text(p.Value)
		}
		gate := s.BindProcess(Binding{SessionID: "case" + integerString(int64(n))}, env) // Mutating the caller map cannot change its selected process contract.
		env["CC_HAHA_TRACE_API_CALLS"] = "0"
		env["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
		capture := gate(FetchInput{Context: ctx, Method: "POST", URL: "https://fixture.invalid/messages", PlainBody: []byte(`{"model":"fixture-model"}`)})
		want := object(expected[n])
		if (capture != nil) != truthy(want.Get("shouldCapture")) {
			t.Fatalf("Case %d Source=%s native capture=%v", n, expected[n], capture != nil)
		}
		if capture != nil {
			capture.FetchFailure(errors.New("loopback fixture failed"))
			if e = s.Drain(ctx); e != nil {
				t.Fatal(e)
			}
			full, e := s.Full(ctx, "case"+integerString(int64(n)))
			if e != nil {
				t.Fatal(e)
			}
			if len(array(full.Get("calls"))) != 1 || number(object(full.Get("summary")).Get("failedCalls")) != 1 {
				t.Fatalf("Per-process enabled override lost actual write: %s", raw(full))
			}
		}
	}
	if len(s.tails) != 0 {
		t.Fatal("Shared writer retained process tails")
	}
}
