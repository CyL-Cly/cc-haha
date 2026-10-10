package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const streamStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"role\":\"assistant\",\"model\":\"fixture\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"
const streamTool = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_fixture\",\"name\":\"FixtureWrite\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"fixture\\\"}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
const streamText = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"visible 中文\"}}\n\n"
const streamTerminal = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func preparedStream() *MessagesRequest {
	return &MessagesRequest{Path: "/v1/messages?beta=true", Body: []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde;"}],"model":"fixture","stream":true}`)}
}

type observedStreamBody struct {
	io.Reader
	closed bool
}

func (body *observedStreamBody) Close() error { body.closed = true; return nil }

func streamClient(body io.ReadCloser, status int, inspect func(*http.Request)) *Anthropic {
	return NewAnthropic(AnthropicConfig{APIKey: "fixture-key", Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if inspect != nil {
			inspect(request)
		}
		return &http.Response{StatusCode: status, Body: body}, nil
	})}})
}

func TestStreamSendsSignedBytesAndCommitsOnlyTerminalMessage(t *testing.T) {
	prepared := preparedStream()
	body := &observedStreamBody{Reader: strings.NewReader(streamStart + streamTool + streamTerminal)}
	requests, events, commits := 0, 0, 0
	client := streamClient(body, 200, func(request *http.Request) {
		requests++
		raw, _ := io.ReadAll(request.Body)
		if !bytes.Equal(raw, prepared.Body) || request.Method != "POST" || request.URL.String() != "https://api.anthropic.com/v1/messages?beta=true" {
			t.Fatalf("transport changed signed request bytes or SDK route: %s %s %s", request.Method, request.URL, raw)
		}
		if request.Header.Get("x-api-key") != "fixture-key" || request.Header.Get("anthropic-version") != "2023-06-01" || request.Header.Get("content-type") != "application/json" {
			t.Fatal("required request headers changed", request.Header)
		}
	})
	message, err := client.StreamMessage(context.Background(), prepared, func(update protocol.StreamUpdate) error {
		if update.Event != nil {
			events++
			if commits != 0 {
				t.Fatal("assistant committed before all raw events")
			}
		}
		if update.Message != nil {
			commits++
		}
		return nil
	})
	if err != nil || message == nil || len(message.Blocks()) != 1 || commits != 1 || events != 6 || requests != 1 || !body.closed {
		t.Fatalf("stream boundary changed: message=%v err=%v commits=%d events=%d requests=%d closed=%v", message, err, commits, events, requests, body.closed)
	}
}

func TestStreamHeaderOverridesAndExplicitAuthOmissions(t *testing.T) {
	for _, test := range []struct {
		name      string
		removed   []string
		headers   http.Header
		key, auth string
	}{
		{"override credentials", nil, http.Header{"X-Api-Key": {"replacement"}, "Authorization": {"Bearer replaced"}, "Anthropic-Beta": {"a,b"}}, "replacement", "Bearer replaced"},
		{"omit key", []string{"x-api-key"}, nil, "", ""},
		{"omit both", []string{"X-Api-Key", "Authorization"}, nil, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := preparedStream()
			prepared.RemovedHeaders, prepared.Headers = test.removed, test.headers
			client := streamClient(io.NopCloser(strings.NewReader(streamStart+streamTool+streamTerminal)), 200, func(request *http.Request) {
				if request.Header.Get("x-api-key") != test.key || request.Header.Get("authorization") != test.auth || request.Header.Get("anthropic-beta") != test.headers.Get("anthropic-beta") {
					t.Fatal("defaults replaced caller credential decision", request.Header)
				}
			})
			if _, err := client.StreamMessage(context.Background(), prepared, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	client := streamClient(io.NopCloser(strings.NewReader(streamStart+streamTool+streamTerminal)), 200, func(request *http.Request) {
		if request.Header.Get("x-api-key") != "trimmed" || request.Header.Get("authorization") != "Bearer token" {
			t.Fatal("Fetch credential whitespace differed", request.Header)
		}
	})
	client.config.APIKey, client.config.AuthToken = "\ttrimmed\r\n", "token\t"
	if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err != nil {
		t.Fatal(err)
	}
	client.config.APIKey, client.config.AuthToken = "", ""
	if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err == nil || !strings.HasPrefix(err.Error(), "Could not resolve authentication method.") {
		t.Fatal("missing credentials bypassed SDK validation", err)
	}
}

func TestStreamFailuresNeverRetryOrExposeLocalTools(t *testing.T) {
	for _, suffix := range []string{"", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failure\"}}\n\n", strings.ReplaceAll(streamTerminal, "tool_use", "max_tokens")} {
		body := &observedStreamBody{Reader: strings.NewReader(streamStart + streamTool + streamText + suffix)}
		calls := 0
		client := streamClient(body, 200, func(*http.Request) { calls++ })
		message, err := client.StreamMessage(context.Background(), preparedStream(), func(update protocol.StreamUpdate) error {
			if update.Message != nil {
				for _, block := range update.Message.Blocks() {
					if block.String("type") == "tool_use" {
						t.Fatal("failed response exposed an executable tool")
					}
				}
			}
			return nil
		})
		if err == nil || message == nil || calls != 1 || !body.closed {
			t.Fatalf("failure lost state, retried or leaked response: %v %v calls=%d closed=%v", message, err, calls, body.closed)
		}
	}
	for _, raw := range []string{`{"message":"denied"}`, `{"error":{"message":"denied"}}`} {
		body := &observedStreamBody{Reader: strings.NewReader(raw)}
		calls := 0
		_, err := streamClient(body, 429, func(*http.Request) { calls++ }).StreamMessage(context.Background(), preparedStream(), nil)
		if err == nil || !strings.HasPrefix(err.Error(), "429 ") || calls != 1 || !body.closed {
			t.Fatal("HTTP failure was retried or left body open", err, calls, body.closed)
		}
	}
}

func TestStreamConsumerFailureClosesResponseAndPreservesCause(t *testing.T) {
	cause := errors.New("journal unavailable")
	body := &observedStreamBody{Reader: strings.NewReader(streamStart + streamTool + streamTerminal)}
	calls := 0
	_, err := streamClient(body, 200, nil).StreamMessage(context.Background(), preparedStream(), func(protocol.StreamUpdate) error { calls++; return cause })
	if !errors.Is(err, cause) || calls != 1 || !body.closed {
		t.Fatal("consumer failure was replaced, re-delivered or leaked", err, calls, body.closed)
	}
}

func TestStreamFetchTimerEndsAtHeaders(t *testing.T) {
	body := &observedStreamBody{Reader: strings.NewReader(streamStart + streamTool + streamTerminal)}
	client := streamClient(body, 200, nil)
	millis := 100.0
	client.config.TimeoutMillis = &millis
	delayed := false
	_, err := client.StreamMessage(context.Background(), preparedStream(), func(update protocol.StreamUpdate) error {
		if update.Event != nil && !delayed {
			delayed = true
			time.Sleep(250 * time.Millisecond)
		}
		return nil
	})
	if err != nil || !delayed || !body.closed {
		t.Fatal("fetch timer remained active while consuming body", err)
	}
}

func TestStreamAbortUnblocksHTTPReaderAndDiscardsCompletedTool(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.RequestURI() != "/anthropic/v1/messages?beta=true" {
			t.Errorf("SDK base path changed: %s", request.URL)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, streamStart+streamTool+streamText)
		w.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", BaseURL: server.URL + "/anthropic/v1/", Timeout: time.Second})
	message, err := client.StreamMessage(ctx, preparedStream(), func(update protocol.StreamUpdate) error {
		var part struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(update.Event, &part)
		if part.Type == "content_block_delta" && bytes.Contains(update.Event, []byte("visible")) {
			cancel()
		}
		return nil
	})
	var streamErr *protocol.StreamError
	if !errors.As(err, &streamErr) || streamErr.Code != protocol.StreamAborted || !errors.Is(err, context.Canceled) || message == nil || requests.Load() != 1 {
		t.Fatal("HTTP abort changed typed stream recovery or retried", message, err, requests.Load())
	}
	for _, block := range message.Blocks() {
		if block.String("type") == "tool_use" {
			t.Fatal("cancelled HTTP response committed a local tool")
		}
	}
}

func TestStreamRequestFailuresDoNotReachCommit(t *testing.T) {
	if _, err := NewAnthropic(AnthropicConfig{}).StreamMessage(context.Background(), nil, nil); err == nil {
		t.Fatal("nil prepared request accepted")
	}
	for _, timeout := range []float64{math.NaN(), math.Inf(1), 0.5, -1} {
		client := streamClient(io.NopCloser(strings.NewReader("")), 200, func(*http.Request) { t.Fatal("invalid timeout reached transport") })
		client.config.TimeoutMillis = &timeout
		if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err == nil {
			t.Fatal("invalid timeout accepted", timeout)
		}
	}
	for _, test := range []struct {
		cause   error
		context func() context.Context
		want    string
	}{
		{errors.New("disconnected"), context.Background, "Connection error."},
		{timedOut{}, context.Background, "Request timed out."},
		{context.Canceled, func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, "Request was aborted."},
		{context.DeadlineExceeded, func() context.Context {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			cancel()
			return ctx
		}, "Request timed out."},
	} {
		client := NewAnthropic(AnthropicConfig{APIKey: "fixture", Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, test.cause })}})
		if _, err := client.StreamMessage(test.context(), preparedStream(), nil); err == nil || err.Error() != test.want {
			t.Fatal("fetch failure changed classification", err, test.want)
		}
	}
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) { return nil, request.Context().Err() })}})
	if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err == nil || err.Error() != "Request timed out." {
		t.Fatal("nonpositive explicit transport timeout changed", err)
	}
	client.config.BaseURL = "%invalid"
	if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err == nil {
		t.Fatal("malformed URL reached transport")
	}
	body := &observedStreamBody{Reader: brokenBody{}}
	if _, err := streamClient(body, 500, nil).StreamMessage(context.Background(), preparedStream(), nil); err == nil || !body.closed {
		t.Fatal("HTTP error body read failure was swallowed or leaked", err, body.closed)
	}
}

func TestStreamFinalHeaderBoundaryRunsAfterValidationAndBeforeFetch(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		prepare func(*Anthropic) context.Context
	}{
		{"missing authentication", func(client *Anthropic) context.Context { client.config.APIKey = ""; return context.Background() }},
		{"invalid timeout", func(client *Anthropic) context.Context {
			millis := math.NaN()
			client.config.TimeoutMillis = &millis
			return context.Background()
		}},
		{"pre-aborted", func(*Anthropic) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := streamClient(io.NopCloser(strings.NewReader("")), 200, func(*http.Request) { t.Fatal("invalid request reached fetch") })
			client.config.FinalizeHeaders = func(string, http.Header) (http.Header, error) {
				t.Fatal("invalid request allocated final headers")
				return nil, nil
			}
			if _, err := client.StreamMessage(fixture.prepare(client), preparedStream(), nil); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	finalized := false
	client := streamClient(io.NopCloser(strings.NewReader(streamStart+streamTool+streamTerminal)), 200, func(request *http.Request) {
		if !finalized || request.Header.Get("X-Final-Fixture") != "present" || request.Header.Get("Authorization") != "Bearer rotated-token" {
			t.Fatal("transport bypassed final header boundary", request.Header)
		}
	})
	client.config.FinalizeHeaders = func(url string, headers http.Header) (http.Header, error) {
		if finalized || url != "https://api.anthropic.com/v1/messages?beta=true" || headers.Get("Content-Type") != "application/json" {
			t.Fatal("final header callback order/count changed", url, headers)
		}
		finalized = true
		result := headers.Clone()
		result.Set("X-Final-Fixture", "present")
		result.Set("Authorization", "Bearer rotated-token")
		return result, nil
	}
	if _, err := client.StreamMessage(context.Background(), preparedStream(), nil); err != nil || !finalized {
		t.Fatal("valid request lost final header callback", err)
	}
}
