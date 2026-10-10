package tracecapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestProtocolObserverAgainstActualSource(t *testing.T) {
	cases := []protocol.JSONObject{}
	request := obj("model", "fixture", "max_completion_tokens", 64, "messages", []any{obj("content", "PRIVATE_BODY")})
	budget := obj("source", "explicit", "requested", 32000, "effective", 64, "field", "max_completion_tokens", "reason", "hard_limit", "hardLimit", 64)
	largeDelta := "data:" + string(raw(obj("choices", []any{obj("delta", obj("content", strings.Repeat("PRIVATE_BODY", 100)))}))) + "\r\n\r\n"
	ops := []protocol.JSONObject{}
	for n := 0; n < 1100; n++ {
		ops = append(ops, obj("bytes", largeDelta))
	}
	ops = append(ops, obj("bytes", "data:{\"choices\":[{\"index\":0,\"finish_reason\":\"length\"}]}\r\n\r\ndata: {\"choices\":[],\r\ndata: \"usage\":{\"prompt_tokens\":10,\"completion_tokens\":64,\"completion_tokens_details\":{\"reasoning_tokens\":32,\"secret\":\"PRIVATE_BODY\"}}}\r\n\r\ndata:[DONE]\r\n\r\n"), obj("finish", "eof"))
	cases = append(cases, obj("protocol", "openai_chat", "request", request, "budget", budget, "operations", ops))
	cases = append(cases, obj("protocol", "openai_responses", "request", obj(), "operations", []any{obj("bytes", "data:"+strings.Repeat("x", ProtocolFrameChars+10)+"\n\ndata:{oops}\n\nevent:response.incomplete\ndata:{\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":4,\"output_tokens\":8}}}\n\n"), obj("finish", "eof")}))
	frame := "event:response.failed\r\ndata:{\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"PRIVATE_BODY\"}}}\r\n\r\n"
	ops = nil
	for _, c := range frame {
		ops = append(ops, obj("bytes", string(c)))
	}
	cases = append(cases, obj("protocol", "openai_responses", "request", obj("max_output_tokens", 128), "operations", ops))
	cases = append(cases, obj("protocol", "openai_chat", "request", obj(), "operations", []any{obj("json", obj("choices", []any{obj("message", obj("content", "PRIVATE_BODY"), "finish_reason", "tool_calls")}, "usage", obj("completion_tokens", 7))), obj("finish", "non_stream")}))
	for _, transport := range []string{"eof", "error", "cancelled", "non_stream"} {
		cases = append(cases, obj("protocol", "openai_chat", "request", obj("max_tokens", 3, "max_output_tokens", 16), "operations", []any{obj("bytes", "data:{\"choices\":[{\"finish_reason\":\"stop\"}]}"), obj("finish", transport), obj("bytes", "\n\ndata:[DONE]\n\n"), obj("finish", "eof")}))
	}
	cases = append(cases, obj("protocol", "openai_responses", "request", obj(), "budget", obj("source", "default", "requested", 32000, "effective", 32000, "field", "max_output_tokens", "reason", "upstream_default"), "operations", []any{obj("bytes", "event:error\ndata:{\"type\":\"error\",\"code\":\"server_error\",\"message\":\"PRIVATE_BODY\"}\n\n")}))
	cases = append(cases, obj("protocol", "openai_chat", "request", obj("max_tokens", -1, "max_output_tokens", 1.5), "operations", []any{obj("json", json.RawMessage(`{"choices":[{"index":"0","finish_reason":"wrong"},{"index":0,"finish_reason":"length"}],"usage":{"prompt_tokens":1e999,"completion_tokens":2.2,"total_tokens":9007199254740992,"prompt_tokens_details":{"cached_tokens":0,"reasoning_tokens":4,"other":77}}}`))}))
	cases = append(cases, obj("protocol", "openai_responses", "request", obj(), "operations", []any{obj("bytes", []int{0xef, 0xbb}), obj("bytes", []int{0xbf, 0x0a, 0x0a}), obj("json", obj("type", "response.error", "code", "bad code with secret", "error", obj("type", "api_error", "code", "rate_limit", "message", "PRIVATE_BODY"))), obj("finish", "error")}))
	expected := array(source(t, obj("action", "protocolObserver", "cases", cases)).Get("observations"))
	for n, row := range cases {
		o, e := NewProtocolObserver(text(row.Get("protocol")), row.Get("request"), object(row.Get("budget")))
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range array(row.Get("operations")) {
			op := object(v)
			if b := op.Get("bytes"); b != nil {
				if b[0] == '"' {
					o.Push([]byte(text(b)))
				} else {
					values := array(b)
					bytes := make([]byte, len(values))
					for n, v := range values {
						bytes[n] = byte(number(v))
					}
					o.Push(bytes)
				}
			}
			if op.Get("json") != nil {
				o.ObserveJSON(op.Get("json"), text(op.Get("event")))
			}
			if op.Get("finish") != nil {
				o.Finish(text(op.Get("finish")))
			}
		}
		t.Run(integerString(int64(n)), func(t *testing.T) { compare(t, expected[n], o.Snapshot()) })
		if bytes.Contains(raw(o.Snapshot()), []byte("PRIVATE_BODY")) {
			t.Fatal("Protocol summary retained content")
		}
	}
}
func TestProxyProducerAgainstActualSource(t *testing.T) {
	anthropic := obj("model", "model", "max_tokens", 32, "messages", []any{obj("role", "user", "content", "run")})
	provider := obj("id", "provider-id", "name", "Fixture", "format", "openai_chat")
	headers := obj("Content-Type", "application/json", "Authorization", "Bearer fake-token", "X-Custom", "yes")
	base := obj("model", "model", "protocol", "openai_chat", "provider", provider, "anthropic", anthropic, "upstream", obj("model", "model", "max_completion_tokens", 32, "messages", []any{obj("role", "user", "content", "run")}), "url", "https://fixture.invalid/chat/completions", "headers", headers, "budget", obj("source", "explicit", "requested", 32, "effective", 32, "field", "max_completion_tokens", "reason", "explicit_request"))
	cases := []protocol.JSONObject{}
	appendCase := func(fields ...any) {
		n := len(cases)
		row := base.Clone()
		row.Set("id", jstr("proxy-"+integerString(int64(n))))
		row.Set("sessionId", jstr("session-"+integerString(int64(n))))
		row.Assign(obj(fields...))
		cases = append(cases, row)
	}
	appendCase("status", 200, "upstreamResponse", obj("choices", []any{obj("index", 0, "finish_reason", "stop", "message", obj("content", "done"))}, "usage", obj("prompt_tokens", 4, "completion_tokens", 5)), "anthropicResponse", obj("type", "message", "content", []any{obj("type", "text", "text", "done")}, "usage", obj("input_tokens", 4, "output_tokens", 5)))
	appendCase("status", 401, "upstreamResponse", `{"error":{"type":"api_error","code":"invalid_api_key","message":"Bearer fake-token"}}`, "anthropicResponse", obj("type", "error", "error", obj("type", "api_error", "message", "Upstream 401")))
	appendCase("error", "Bearer failureFake token")
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n"
	appendCase("status", 200, "upstreamChunks", []any{"data:{\"choices\":[{\"index\":0,\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata:[DONE]\n\n"}, "anthropicChunks", []any{sse}, "transport", "eof", "delivery", "eof")
	appendCase("protocol", "openai_responses", "upstream", obj("model", "model", "max_output_tokens", 32, "input", []any{}), "status", 200, "upstreamChunks", []any{"event:response.incomplete\ndata:{\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"}, "anthropicChunks", []any{sse}, "transport", "eof", "delivery", "cancelled", "error", "Stream cancelled")
	appendCase("status", 200, "anthropicChunks", []any{"prefix", strings.Repeat("a", StreamCaptureBytes)}, "transport", "eof", "delivery", "eof")
	appendCase("protocol", "anthropic", "status", 200, "upstreamResponse", `{"type":"message","content":[{"type":"text","text":"done"}]}`)
	appendCase("protocol", "anthropic", "status", 401, "upstreamResponse", `{"type":"error","error":{"type":"authentication_error"}}`)
	appendCase("protocol", "anthropic", "status", 200, "anthropicChunks", []any{sse}, "delivery", "cancelled", "error", "Stream cancelled")
	appendCase("protocol", "anthropic", "error", "Upstream failed")
	for _, entry := range []string{"", "cli", "sdk-cli", "claude-desktop"} {
		env := map[string]string{}
		if entry != "" {
			env["CLAUDE_CODE_ENTRYPOINT"] = entry
		}
		appendCase("protocol", "anthropic", "status", 200, "upstreamResponse", `{"type":"message","content":[{"type":"text","text":"trace default enabled"}]}`, "settingsEnabled", true, "environment", env)
	}
	expected := array(source(t, obj("action", "proxyProducer", "cases", cases)).Get("observations"))
	stamp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, e := New(Options{Scope: fixtureHome(t), Now: func() time.Time { return stamp }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	for n, row := range cases {
		if len(array(object(expected[n]).Get("calls"))) == 0 {
			t.Fatalf("actual Source did not produce a proxy call for profile %d", n)
		}
		id := text(row.Get("id"))
		s.o.UUID = func() string { return id }
		info := &ProviderInfo{ID: ptr("provider-id"), Name: "Fixture", Format: "openai_chat"}
		env := map[string]string{"CC_HAHA_TRACE_API_CALLS": "1"}
		if row.Get("environment") != nil {
			// Unmarshal into a fresh map so absent keys stay absent.
			env = map[string]string{}
			_ = json.Unmarshal(row.Get("environment"), &env)
			enabled := string(row.Get("settingsEnabled")) == "true"
			if _, err := s.UpdateSettings(context.Background(), &enabled); err != nil {
				t.Fatal(err)
			}
		}
		capture, e := s.BeginProxy(ProxyInput{Binding: Binding{SessionID: text(row.Get("sessionId")), Provider: info}, Model: text(row.Get("model")), Protocol: text(row.Get("protocol")), AnthropicRequest: object(row.Get("anthropic")), UpstreamRequest: row.Get("upstream"), OutputBudget: object(row.Get("budget")), RequestHeaders: object(row.Get("headers")), Fetch: FetchInput{Context: context.Background(), Method: "POST", URL: text(row.Get("url"))}}, env)
		if e != nil || capture == nil {
			t.Fatalf("Start real proxy capture: %v", e)
		}
		for _, v := range array(row.Get("upstreamChunks")) {
			capture.PushUpstream([]byte(text(v)))
		}
		for _, v := range array(row.Get("anthropicChunks")) {
			capture.PushAnthropic([]byte(text(v)))
		}
		if row.Get("transport") != nil {
			capture.FinishTransport(text(row.Get("transport")))
		}
		outcome := ProxyOutcome{UpstreamResponse: row.Get("upstreamResponse"), AnthropicResponse: row.Get("anthropicResponse"), Headers: http.Header{}, StreamDelivery: text(row.Get("delivery"))}
		if row.Get("status") != nil {
			outcome.ResponseStatus = ptr(int(number(row.Get("status"))))
		}
		if row.Get("error") != nil {
			outcome.Error = errors.New(text(row.Get("error")))
		}
		capture.Complete(outcome)
		if e = s.Drain(context.Background()); e != nil {
			t.Fatal(e)
		}
		full, e := s.Full(context.Background(), text(row.Get("sessionId")))
		if e != nil {
			t.Fatal(e)
		}
		want := stripProxyDynamic(expected[n])
		actual := stripProxyDynamic(raw(full))
		t.Run(integerString(int64(n)), func(t *testing.T) { compare(t, want, actual) })
	}
}
func stripProxyDynamic(v json.RawMessage) json.RawMessage {
	var value any
	_ = json.Unmarshal(v, &value)
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			delete(x, "stack")
			if _, isEvent := x["phase"]; isEvent {
				if _, hasStamp := x["timestamp"]; hasStamp {
					delete(x, "id")
				}
			}
			for _, v := range x {
				visit(v)
			}
		case []any:
			for _, v := range x {
				visit(v)
			}
		}
	}
	visit(value)
	return raw(value)
}
