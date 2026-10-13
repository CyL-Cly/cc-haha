package provider

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSyncOpenAIFastTierReachesPhysicalHTTPAndClearsWhenOff(t *testing.T) {
	captures := make(chan protocol.JSONObject, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body protocol.JSONObject
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if r.Header.Get("Authorization") != "Bearer fake-priority-token" {
			t.Error("token/header consumer")
		}
		captures <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, openAISSE("response.completed", `{"response":{"id":"private-fast","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"physical priority response"}]}],"usage":{"input_tokens":2,"output_tokens":3}}}`))
	}))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL)
	exchange := metadataExchangeFunc(func(request *http.Request) (*HTTPExchangeResponse, error) {
		if request.URL.String() != providerproxy.OpenAICodexEndpoint {
			t.Error("official route changed", request.URL)
		}
		// Substitute only the network destination: the same body and raw HTTP
		// implementation cross a real socket.
		copy := request.Clone(request.Context())
		copy.URL = endpoint
		copy.Host = endpoint.Host
		return (&RawHTTP1Exchange{}).Do(copy)
	})
	for _, flag := range []string{"1", "0"} {
		client := NewAnthropic(AnthropicConfig{APIKey: providerproxy.OpenAIOAuthDummyKey, Timeout: 5 * time.Second, HTTPExchange: exchange, OpenAICodex: &OpenAICodexConfig{Env: map[string]string{"CC_HAHA_OPENAI_FAST_MODE": flag, "CC_HAHA_OPENAI_REQUEST_COMPRESSION": "off"}, FreshTokens: func(context.Context) (protocol.JSONObject, error) {
			v := protocol.NewJSONObject()
			_ = v.UnmarshalJSON([]byte(`{"accessToken":"fake-priority-token"}`))
			return v, nil
		}}})
		result, cancel, e := client.OpenPreparedResponse(context.Background(), &MessagesRequest{Path: "/v1/messages", Body: []byte(`{"model":"gpt-6-sol","max_tokens":64,"messages":[{"role":"user","content":"private task"}],"stream":false}`)})
		if e != nil {
			t.Fatal(e)
		}
		actual, e := io.ReadAll(result.Response.Body)
		result.Response.Body.Close()
		cancel()
		if e != nil || !json.Valid(actual) {
			t.Fatal(string(actual), e)
		}
		capture := <-captures
		if (capture.String("service_tier") == "priority") != (flag == "1") {
			t.Fatal(flag, string(mustOpenAIJSON(capture)))
		}
	}
}
