package imruntime

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDingTalkActualSDKGatewayErrorsAndIgnoredConfiguredEndpoint(t *testing.T) {
	credentials := object(raw(map[string]any{"clientId": true, "clientSecret": 17, "endpoint": "https://must-not-be-used.invalid"}))
	for _, spec := range []struct {
		status int
		body   string
	}{{200, `null`}, {200, `false`}, {200, `""`}, {200, `{}`}, {200, `{"endpoint":"wss://private.invalid/socket"}`}, {200, `{"endpoint":true,"ticket":9007199254740993}`}, {403, `{"message":"denied"}`}, {500, `broken`}, {200, `broken`}} {
		t.Run(spec.body, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.0/gateway/connections/open" {
					t.Errorf("wrong fixed Source gateway path %s", r.URL.Path)
				}
				w.WriteHeader(spec.status)
				_, _ = w.Write([]byte(spec.body))
			}))
			defer peer.Close()
			want := runDingTalkSource(t, "dingtalk-stream.fixture.ts", map[string]any{"base": peer.URL, "credentials": credentials, "gateway": true})
			base, _ := url.Parse(peer.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
			defer api.Close()
			address, value, e := api.Gateway(context.Background())
			var out protocol.JSONObject
			if e != nil {
				out = protocol.NewJSONObject(dingTalkField("error", e.Error()))
			} else {
				out = protocol.NewJSONObject(dingTalkField("value", protocol.NewJSONObject(dingTalkField("url", address), dingTalkField("endpoint", value))))
			}
			exactJSON(t, raw(out), raw(want), "actual SDK fixed gateway/error")
		})
	}
	for _, spec := range []string{`{}`, `{"clientId":0,"clientSecret":"s"}`, `{"clientId":"i","clientSecret":false}`, `{"clientId":-0,"clientSecret":true}`} {
		api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object([]byte(spec))})
		if _, e := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api}); e == nil || e.Error() != "clientId or clientSecret is null" {
			api.Close()
			t.Fatalf("Source credential gate %s: %v", spec, e)
		}
		api.Close()
	}
}
