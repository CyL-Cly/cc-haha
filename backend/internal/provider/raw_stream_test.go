package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRawStreamPreservesPreparedBytesMetadataAndSDKDispatch(t *testing.T) {
	wire := "event: ping\ndata: invalid\n\nevent: unknown\ndata: [DONE]\n\ndata: {}\n\nevent: completion\ndata: {\"type\":\"not_completion\",\"number\":1e400,\"surrogate\":\"\\ud800\"}\n\nevent: message_start\ndata: null\n\nevent: message_delta\ndata: [1,2]\n\nevent: message_stop\ndata: -0\n\n"
	body := &observedStreamBody{Reader: strings.NewReader(wire)}
	prepared := preparedStream()
	calls := 0
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(request.Body)
		if !bytes.Equal(raw, prepared.Body) {
			t.Fatal("signed body changed", string(raw))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Request-Id": {"first", "second"}, "X-Fixture": {"before"}}, Body: body}, nil
	})}})
	stream, err := client.OpenMessagesStream(context.Background(), prepared)
	if err != nil || stream.RequestID == nil || *stream.RequestID != "second" || stream.Headers.Get("X-Fixture") != "before" {
		t.Fatal(stream, err)
	}
	for _, want := range []string{`{"type":"not_completion","number":1e400,"surrogate":"\ud800"}`, `null`, `[1,2]`, `-0`} {
		got, err := stream.Next()
		if err != nil || string(got) != want {
			t.Fatal(string(got), err, want)
		}
	}
	if _, err := stream.Next(); err != io.EOF || stream.AbortCause() != nil || !body.closed || calls != 1 {
		t.Fatal("normal EOF/ownership changed", err, stream.AbortCause(), calls)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatal("closed iterator restarted", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRawStreamSDKAPIErrorKeepsFullPayloadAndResponseProperties(t *testing.T) {
	for _, test := range []struct{ data, message, payload string }{
		{`{"type":"error","error":{"message":"nested"}}`, `{"type":"error","error":{"message":"nested"}}`, `{"type":"error","error":{"message":"nested"}}`},
		{`{"message":"top","extra":1}`, `top`, `{"message":"top","extra":1}`},
		{`{"message":false}`, `{"message":false}`, `{"message":false}`},
		{`{"message":[1,2]}`, `[1,2]`, `{"message":[1,2]}`},
		{`false`, `(no status code or body)`, `false`},
		{`0`, `(no status code or body)`, `0`},
		{`null`, `"null"`, `"null"`},
		{`bad-json`, `"bad-json"`, `"bad-json"`},
		{``, `(no status code or body)`, `""`},
	} {
		t.Run(test.data, func(t *testing.T) {
			body := &observedStreamBody{Reader: strings.NewReader("event: error\ndata: " + test.data + "\n\n")}
			client := streamClient(body, 200, nil)
			stream, err := client.OpenMessagesStream(context.Background(), preparedStream())
			if err != nil {
				t.Fatal(err)
			}
			stream.Headers.Set("Request-Id", "fixture-id")
			_, err = stream.Next()
			var sdk *SDKStreamError
			if !errors.As(err, &sdk) || sdk.Kind != "sse_api" || sdk.Class != "APIError" || sdk.Name != "Error" || sdk.Message != test.message || string(sdk.Payload) != test.payload || sdk.Status != nil || sdk.RequestID == nil || *sdk.RequestID != "fixture-id" || !body.closed {
				t.Fatal(sdk, err)
			}
			stream.Headers.Set("Request-Id", "changed")
			if sdk.Headers.Get("Request-Id") != "fixture-id" {
				t.Fatal("error metadata aliases mutable response headers")
			}
		})
	}
}

func TestRawStreamHTTPErrorClassesAndUTF16Message(t *testing.T) {
	for _, test := range []struct {
		status int
		class  string
	}{
		{400, "BadRequestError"}, {401, "AuthenticationError"}, {403, "PermissionDeniedError"}, {404, "NotFoundError"}, {409, "ConflictError"}, {422, "UnprocessableEntityError"}, {429, "RateLimitError"}, {500, "InternalServerError"}, {418, "APIError"},
	} {
		body := &observedStreamBody{Reader: strings.NewReader(`{"message":"\ud800","extra":true}`)}
		_, err := streamClient(body, test.status, nil).OpenMessagesStream(context.Background(), preparedStream())
		var sdk *SDKStreamError
		if !errors.As(err, &sdk) || sdk.Class != test.class || sdk.Status == nil || *sdk.Status != test.status || sdk.Kind != "http_api" || !bytes.Contains(sdk.MessageJSON, []byte(`\ud800`)) || !body.closed {
			t.Fatal(test, sdk, err)
		}
	}
	for _, raw := range []string{`false`, `null`, `0`, ``, `not-json`, `{"message":0}`} {
		body := &observedStreamBody{Reader: strings.NewReader("\ufeff" + raw)}
		_, err := streamClient(body, 503, nil).OpenMessagesStream(context.Background(), preparedStream())
		var sdk *SDKStreamError
		if !errors.As(err, &sdk) || sdk.Message != statusError(503, []byte(raw)).Error() || !body.closed {
			t.Fatal(raw, sdk, err)
		}
	}
}

func TestRawStreamJSONFailureEmitsOnlySDKDiagnosticsThenCloses(t *testing.T) {
	body := &observedStreamBody{Reader: strings.NewReader(": previous\n\nevent: message_stop\n\n")}
	client := streamClient(body, 200, nil)
	var diagnostics []any
	client.config.StreamDiagnostic = func(message string, value any) { diagnostics = append(diagnostics, []any{message, value}) }
	stream, err := client.OpenMessagesStream(context.Background(), preparedStream())
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Next()
	var sdk *SDKStreamError
	if !errors.As(err, &sdk) || sdk.Class != "SyntaxError" || sdk.Name != "SyntaxError" || sdk.Status != nil || sdk.Headers != nil || sdk.RequestID != nil || sdk.Payload != nil || sdk.Message != "JSON Parse error: Unexpected EOF" || !body.closed {
		t.Fatal(sdk, err)
	}
	want := []any{[]any{"Could not parse message into JSON:", ""}, []any{"From chunk:", []string{": previous", "event: message_stop"}}}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Fatal(diagnostics, want)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatal(err)
	}
}

func TestRawStreamHTTP204And205HaveThePinnedBunFetchEOFProfile(t *testing.T) {
	for _, status := range []int{204, 205} {
		body := &observedStreamBody{Reader: strings.NewReader("")}
		stream, err := streamClient(body, status, nil).OpenMessagesStream(context.Background(), preparedStream())
		if err != nil {
			t.Fatal("no-body error moved before iterator", err)
		}
		_, err = stream.Next()
		if err != io.EOF || !body.closed {
			t.Fatal(err)
		}
	}
}

func TestRawStreamCancelAndConsumerCloseReleaseActualHTTPBody(t *testing.T) {
	for _, parentAbort := range []bool{true, false} {
		var requests atomic.Int32
		ended := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Request-Id", "http-fixture")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(ended)
		}))
		ctx, cancel := context.WithCancel(context.Background())
		client := NewAnthropic(AnthropicConfig{APIKey: "fixture", BaseURL: server.URL, Timeout: time.Second})
		stream, err := client.OpenMessagesStream(ctx, preparedStream())
		if err != nil {
			cancel()
			server.Close()
			t.Fatal(err)
		}
		raw, err := stream.Next()
		if err != nil || !json.Valid(raw) {
			t.Fatal(string(raw), err)
		}
		result := make(chan error, 1)
		go func() { _, err := stream.Next(); result <- err }()
		if parentAbort {
			cancel()
		} else {
			_ = stream.Close()
		}
		select {
		case err := <-result:
			if parentAbort && err != io.EOF {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked reader did not release")
		}
		if parentAbort && !errors.Is(stream.AbortCause(), context.Canceled) {
			t.Fatal(stream.AbortCause())
		}
		select {
		case <-ended:
		case <-time.After(time.Second):
			t.Fatal("request was not cancelled")
		}
		if requests.Load() != 1 {
			t.Fatal("transport retried", requests.Load())
		}
		cancel()
		server.Close()
	}
}

type rawRequestTimeout struct{}

func (rawRequestTimeout) Error() string { return "fixture fetch timeout" }
func (rawRequestTimeout) Timeout() bool { return true }

func TestRawRequestFailuresRetainSDKClassesAndConnectionCause(t *testing.T) {
	cause := errors.New("fixture connection failed")
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}})
	_, err := client.OpenMessagesStream(context.Background(), preparedStream())
	var sdk *SDKStreamError
	if !errors.As(err, &sdk) || sdk.Class != "APIConnectionError" || sdk.Kind != "request_connection" || !errors.Is(err, cause) || sdk.Headers != nil || sdk.Payload != nil {
		t.Fatal(sdk, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.OpenMessagesStream(ctx, preparedStream())
	if !errors.As(err, &sdk) || sdk.Class != "APIUserAbortError" || sdk.Cause != nil {
		t.Fatal(sdk, err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = client.OpenMessagesStream(ctx, preparedStream())
	if !errors.As(err, &sdk) || sdk.Class != "APIUserAbortError" || sdk.Cause != nil {
		t.Fatal(sdk, err)
	}
	client.config.HTTPClient.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, rawRequestTimeout{} })
	_, err = client.OpenMessagesStream(context.Background(), preparedStream())
	if !errors.As(err, &sdk) || sdk.Class != "APIConnectionTimeoutError" || sdk.Cause != nil {
		t.Fatal(sdk, err)
	}
	if _, err := client.OpenMessagesStream(context.Background(), nil); err == nil {
		t.Fatal("missing prepared body accepted")
	}
	client.config.APIKey = ""
	if _, err := client.OpenMessagesStream(context.Background(), preparedStream()); err == nil || !strings.Contains(err.Error(), "Could not resolve authentication") {
		t.Fatal(err)
	}
}

func TestRawCallerAbortWinsAfterSDKTimerBeforeFetchRejection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", Timeout: time.Millisecond, HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		<-request.Context().Done() // the SDK's own timer fired
		cancel()                   // caller signal then aborts before fetch rejects
		return nil, context.Canceled
	})}})
	_, err := client.OpenMessagesStream(ctx, preparedStream())
	var sdk *SDKStreamError
	if !errors.As(err, &sdk) || sdk.Class != "APIUserAbortError" || sdk.Message != "Request was aborted." || calls != 1 {
		t.Fatal(sdk, err, calls)
	}
}
