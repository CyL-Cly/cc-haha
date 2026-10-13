package providerproxytransport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

const imagePrompt = `{"model":"deepseek-v4-flash","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"Describe it"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"ZmFrZQ=="}}]}]}`
const imageReply = `{"id":"reply","choices":[{"index":0,"message":{"content":"result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`

func TestImageRefusalRetriesActualTransportAndExpiresByEndpointModel(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		mu.Lock()
		requests = append(requests, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(raw), "image_url") {
			w.WriteHeader(422)
			fmt.Fprint(w, `{"error":{"message":"This is not a multimodal model"}}`)
			return
		}
		fmt.Fprint(w, imageReply)
	}))
	defer peer.Close()
	now := int64(1000)
	events := []ImageInputDisabled{}
	type requestContextKey struct{}
	requestContext := context.WithValue(context.Background(), requestContextKey{}, "original-session-context")
	config := providerproxy.ProxyConfig{ID: "image-provider", APIFormat: "openai_chat", APIKey: "fake", BaseURL: peer.URL}
	var service *Service
	var err error
	service, err = New(Options{Provider: func(context.Context, string) (*providerproxy.ProxyConfig, error) { return &config, nil }, Settings: func(context.Context) (protocol.JSONObject, error) {
		return object([]byte(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
	}, Environment: func() map[string]string { return map[string]string{} }, NowMillis: func() int64 { return now }, OnImageInputDisabled: func(ctx context.Context, event ImageInputDisabled) {
		if ctx.Value(requestContextKey{}) != "original-session-context" || service.images.Mode(config.BaseURL+"/v1/chat/completions", event.Model, now) != "text_only" {
			t.Error("image diagnostic lost original accepted context")
		}
		events = append(events, event)
	}})
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		req, _ := http.NewRequest("POST", "http://127.0.0.1/proxy/v1/messages", strings.NewReader(imagePrompt))
		req = req.WithContext(requestContext)
		req.Header.Set("X-Claude-Code-Session-Id", "actual-image-session")
		response, err := service.Handle(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "result") {
			t.Fatal(response.StatusCode, string(raw), err)
		}
	}
	run()
	if len(events) != 1 || events[0] != (ImageInputDisabled{ProviderID: "image-provider", Model: "deepseek-v4-flash", SessionID: "actual-image-session", Status: 422}) {
		t.Fatal("successful vision downgrade diagnostic", events)
	}
	mu.Lock()
	if len(requests) != 2 || !strings.Contains(requests[0], "image_url") || strings.Contains(requests[1], "image_url") || !strings.Contains(requests[1], "the upstream rejected image input for this model") {
		t.Fatal(requests)
	}
	mu.Unlock()
	run()
	if len(events) != 1 {
		t.Fatal("cached text request emitted a new disable diagnostic", events)
	}
	mu.Lock()
	if len(requests) != 3 || strings.Contains(requests[2], "image_url") {
		t.Fatal(requests)
	}
	mu.Unlock()
	now += 30 * 60 * 1000
	run()
	if len(events) != 2 {
		t.Fatal("expired vision probe did not record renewed rejection", events)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 || !strings.Contains(requests[3], "image_url") {
		t.Fatal(requests)
	}
}

func TestOnlySuccessfulTextResendCanDisableImages(t *testing.T) {
	for _, failure := range []struct {
		status      int
		text        string
		resendFails bool
	}{
		{401, "unsupported image", false}, {429, "unsupported image", false}, {500, "unsupported image", false},
		{400, "unsupported image MIME format", false}, {400, "image size exceeds limit", false},
		{400, "unsupported image", true},
	} {
		t.Run(fmt.Sprintf("%d/%s/%v", failure.status, failure.text, failure.resendFails), func(t *testing.T) {
			calls := 0
			events := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if calls == 2 && failure.resendFails && strings.Contains(string(raw), "image_url") {
					t.Error("resend still contains image")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(failure.status)
				fmt.Fprintf(w, `{"error":{"message":%q}}`, failure.text)
			}))
			defer peer.Close()
			config := providerproxy.ProxyConfig{APIFormat: "openai_chat", BaseURL: peer.URL, APIKey: "fake"}
			service, err := New(Options{Provider: func(context.Context, string) (*providerproxy.ProxyConfig, error) { return &config, nil }, Settings: func(context.Context) (protocol.JSONObject, error) {
				return object([]byte(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
			}, Environment: func() map[string]string { return map[string]string{} }, OnImageInputDisabled: func(context.Context, ImageInputDisabled) { events++ }})
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("POST", "http://127.0.0.1/proxy/v1/messages", strings.NewReader(imagePrompt))
			response, err := service.Handle(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			want := 1
			if failure.resendFails {
				want = 2
			}
			if calls != want || events != 0 || service.images.Mode(peer.URL+"/v1/chat/completions", "deepseek-v4-flash", service.options.NowMillis()) != "vision" {
				t.Fatal("unsafe learning or repeat retry", calls, want)
			}
		})
	}
}
