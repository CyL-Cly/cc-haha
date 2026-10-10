package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAnthropicBaseURL(t *testing.T) {
	for _, fixture := range [][2]string{
		{"https://api.example/anthropic/v1///", "https://api.example/anthropic"},
		{"https://api.example/a%2Fb/v1/", "https://api.example/a%2Fb"},
		{"https://api.example/v1?route=1", "https://api.example/v1?route=1"},
		{"https://api.example/v1#fragment", "https://api.example/v1#fragment"},
		{"https://api.example/v1?", "https://api.example/v1?"},
		{"ftp://api.example/v1", "ftp://api.example/v1"},
		{"https://api.example/v10/", "https://api.example/v10/"},
		{"%invalid", "%invalid"},
	} {
		if got := NormalizeAnthropicBaseURL(fixture[0]); got != fixture[1] {
			t.Errorf("normalize(%q) = %q, want %q", fixture[0], got, fixture[1])
		}
	}
}

func TestMessagesRequestAndResponse(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/anthropic/v1/messages" || r.Method != "POST" {
			t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
		}
		for key, want := range map[string]string{"x-api-key": "fixture-key", "authorization": "Bearer fixture-token", "anthropic-version": "2023-06-01", "content-type": "application/json", "accept": "application/json"} {
			if got := r.Header.Get(key); got != want {
				t.Errorf("header %s = %q, want %q", key, got, want)
			}
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `\u003c`) {
			t.Error("model-visible text must not be HTML-escaped")
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil || payload["system"] != "<中文&>" {
			t.Errorf("wrong payload: %s (%v)", body, err)
		}
		io.WriteString(w, `{"content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"中文"},{"type":"text","text":"done"}],"unknown":{"keep":true}}`)
	}))
	defer server.Close()
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture-key", AuthToken: "fixture-token", BaseURL: server.URL + "/anthropic/v1/", Timeout: time.Second})
	response, err := client.CreateMessage(context.Background(), map[string]any{"system": "<中文&>"})
	if err != nil {
		t.Fatal(err)
	}
	text, err := response.Text()
	if err != nil || text != "中文\ndone" || !strings.Contains(string(response.Raw), `"unknown"`) || requests != 1 {
		t.Fatalf("response lost content: text=%q raw=%s err=%v requests=%d", text, response.Raw, err, requests)
	}
	for _, raw := range []string{`not JSON`, `{}`} {
		if _, err := (MessageResponse{Raw: []byte(raw)}).Text(); err == nil {
			t.Errorf("accepted invalid content %q", raw)
		}
	}
}

func TestTextUsesJavaScriptJoinSemantics(t *testing.T) {
	for _, fixture := range []struct{ raw, want string }{
		{`{"content":[{"type":"text","text":null},{"type":"text","text":1e400},{"type":"text","text":{}},{"type":"text","text":[1,null,true]},{"type":"text"}]}`, "\nInfinity\n[object Object]\n1,,true\n"},
		{`{"content":[{"type":42,"text":"skip"},{"type":"thinking","text":"skip"},{"type":"text","text":"<中文&>"}]}`, "<中文&>"},
		{`{"content":[]}`, ""},
	} {
		got, err := (MessageResponse{Raw: []byte(fixture.raw)}).Text()
		if err != nil || got != fixture.want {
			t.Fatalf("raw=%s text=%q error=%v, want=%q", fixture.raw, got, err, fixture.want)
		}
	}
	for _, raw := range []string{`{"content":null}`, `{"content":{}}`, `{"content":[42]}`} {
		if _, err := (MessageResponse{Raw: []byte(raw)}).Text(); err == nil {
			t.Errorf("accepted invalid content %s", raw)
		}
	}
}

func TestStatusErrorsDoNotRetry(t *testing.T) {
	for _, fixture := range []struct{ body, want string }{
		{`{"error":{"message":"denied","type":"api_error"}}`, `429 {"error":{"message":"denied","type":"api_error"}}`},
		{`{"message":"denied"}`, "429 denied"},
		{`{"message":{"code":1}}`, `429 {"code":1}`},
		{`{"message":null}`, `429 {"message":null}`},
		{`{"message":0}`, `429 {"message":0}`},
		{`{"message":false}`, `429 {"message":false}`},
		{`{"message":""}`, `429 {"message":""}`},
		{`{"message":1e-400}`, `429 {"message":0}`},
		{`{"message":1e400}`, `429 null`},
		{`{"message":[]}`, `429 []`},
		{`{"message":{"value":9007199254740993,"value":1e400,"label":"\u4e2d"}}`, `429 {"value":null,"label":"中"}`},
		{`{"message":"\u4e2d\ud800"}`, "429 中�"},
		{" false \n", "429  false \n"},
		{`1e400`, `429 null`},
		{`"\u4e2d"`, `429 "中"`},
		{"", "429 status code (no body)"},
		{"non-json upstream failure", "429 non-json upstream failure"},
	} {
		var requests int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(429)
			io.WriteString(w, fixture.body)
		}))
		client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, Timeout: time.Second})
		_, err := client.CreateMessage(context.Background(), map[string]any{})
		server.Close()
		if err == nil || err.Error() != fixture.want || requests != 1 {
			t.Fatalf("error=%v requests=%d, want=%s", err, requests, fixture.want)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("body transport failed") }
func (brokenBody) Close() error             { return nil }

type timedOut struct{}

func (timedOut) Error() string { return "timeout" }
func (timedOut) Timeout() bool { return true }

func TestTransportFailures(t *testing.T) {
	for _, fixture := range []struct {
		err  error
		want string
	}{{errors.New("offline"), "Connection error."}, {timedOut{}, "Request timed out."}} {
		client := NewAnthropic(AnthropicConfig{Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, fixture.err })}})
		if _, err := client.CreateMessage(context.Background(), nil); err == nil || err.Error() != fixture.want {
			t.Fatalf("got %v, want %s", err, fixture.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewAnthropic(AnthropicConfig{Timeout: time.Second})
	if _, err := client.CreateMessage(ctx, nil); err == nil || err.Error() != "Request was aborted." {
		t.Fatal(err)
	}
	client = NewAnthropic(AnthropicConfig{Timeout: 0})
	if _, err := client.CreateMessage(context.Background(), nil); err == nil || err.Error() != "Request timed out." {
		t.Fatal(err)
	}
	client = NewAnthropic(AnthropicConfig{BaseURL: "%invalid", Timeout: time.Second})
	if _, err := client.CreateMessage(context.Background(), nil); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	if _, err := client.CreateMessage(context.Background(), make(chan int)); err == nil {
		t.Fatal("unserializable input accepted")
	}
	for _, fixture := range []struct {
		body io.ReadCloser
		want string
	}{
		{brokenBody{}, "body transport failed"},
		{io.NopCloser(strings.NewReader("bad JSON")), "provider response is not valid JSON"},
	} {
		client = NewAnthropic(AnthropicConfig{Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: fixture.body}, nil
		})}})
		if _, err := client.CreateMessage(context.Background(), nil); err == nil || err.Error() != fixture.want {
			t.Fatalf("got %v, want %s", err, fixture.want)
		}
	}
}

func TestSDKTimeoutValidationAndTimerBounds(t *testing.T) {
	for _, fixture := range []struct {
		millis float64
		want   string
	}{
		{math.NaN(), "timeout must be an integer"},
		{math.Inf(1), "timeout must be an integer"},
		{1.5, "timeout must be an integer"},
		{-1, "timeout must be a positive integer"},
		{0, "Request timed out."}, {math.Copysign(0, -1), "Request timed out."},
		{math.MaxInt32 + 1, "Request timed out."}, {1e48, "Request timed out."},
	} {
		calls := 0
		client := NewAnthropic(AnthropicConfig{TimeoutMillis: &fixture.millis, HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}})
		started := time.Now()
		_, err := client.CreateMessage(context.Background(), nil)
		if err == nil || err.Error() != fixture.want || strings.HasPrefix(fixture.want, "timeout must") && calls != 0 || fixture.want == "Request timed out." && calls != 1 || time.Since(started) > time.Second {
			t.Fatalf("millis=%v error=%v calls=%d", fixture.millis, err, calls)
		}
	}
	millis := float64(math.MaxInt32)
	client := NewAnthropic(AnthropicConfig{TimeoutMillis: &millis, HTTPClient: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if _, ok := request.Context().Deadline(); ok {
			t.Error("fetch timer must not impose a response-body deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"content":[]}`))}, nil
	})}})
	if _, err := client.CreateMessage(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSDKFetchTimeoutStopsAfterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"`)
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		io.WriteString(w, `delayed body success"}]}`)
	}))
	defer server.Close()
	millis := float64(75)
	client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, TimeoutMillis: &millis})
	response, err := client.CreateMessage(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	text, err := response.Text()
	if err != nil || text != "delayed body success" {
		t.Fatalf("text=%q error=%v", text, err)
	}
}
