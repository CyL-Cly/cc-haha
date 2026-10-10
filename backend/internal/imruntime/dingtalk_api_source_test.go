package imruntime

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type dingTalkRedirect struct {
	base      *url.URL
	transport http.RoundTripper
}

func (d dingTalkRedirect) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = d.base.Scheme
	r.URL.Host = d.base.Host
	r.Host = d.base.Host
	return d.transport.RoundTrip(r)
}
func TestDingTalkOriginalTokenTextAndAICardPhysicalHTTP(t *testing.T) {
	var mu sync.Mutex
	calls := []protocol.JSONObject{}
	tokens := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		headers := protocol.NewJSONObject()
		for _, name := range []string{"Accept", "Content-Type", "x-acs-dingtalk-access-token", "User-Agent", "Accept-Encoding"} {
			headers.Set(strings.ToLower(name), raw(r.Header.Get(name)))
		}
		mu.Lock()
		calls = append(calls, protocol.NewJSONObject(dingTalkField("method", r.Method), dingTalkField("path", r.URL.Path), dingTalkField("headers", headers), dingTalkField("body", NewText(string(data)))))
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			tokens++
		}
		n := tokens
		mu.Unlock()
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			_, _ = w.Write(raw(map[string]any{"accessToken": "token-" + string(rune('0'+n)), "expireIn": 7200}))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer peer.Close()
	credentials := object(json.RawMessage(`{"clientId":9007199254740993,"clientSecret":"s\ud800"}`))
	start := int64(1770000000123)
	actions := []protocol.JSONObject{}
	add := func(v string) { actions = append(actions, object(json.RawMessage(v))) }
	add(`{"kind":"token"}`)
	add(`{"kind":"token"}`)
	add(`{"kind":"create","id":"card","target":{"type":"user","userId":"U\ud800"},"options":{"callbackRouteKey":"permission","cardParamMap":{"custom":true}}}`)
	actions = append(actions, protocol.NewJSONObject(dingTalkField("kind", "text"), dingTalkField("text", NewText(strings.Repeat("😀中", 1500)))))
	add(`{"kind":"create","id":"raw","target":{"type":"user","userId":true},"options":{"outTrackId":17,"cardParamMap":"😀","callbackRouteKey":true}}`)
	add(`{"kind":"stream","id":"raw","text":"raw-number-id"}`)
	add(`{"kind":"stream","id":"card","text":"text\n|a|b|\n|---|---|\n|1|2|"}`)
	add(`{"kind":"finish","id":"card","text":"final"}`)
	actions = append(actions, protocol.NewJSONObject(dingTalkField("kind", "create"), dingTalkField("id", "later"), dingTalkField("now", start+7200000-60000), dingTalkField("target", object(raw(map[string]any{"type": "group", "openConversationId": "G"}))), dingTalkField("options", object(raw(map[string]any{"outTrackId": "fixed", "cardTemplateId": "custom"})))))
	input := map[string]any{"base": peer.URL, "now": start, "credentials": credentials, "actions": actions}
	source := runDingTalkSource(t, "dingtalk-api.fixture.ts", input)
	mu.Lock()
	wantCalls := append([]protocol.JSONObject{}, calls...)
	calls = nil
	tokens = 0
	mu.Unlock()
	// Prove the Source capture itself has deterministic original wire replay.
	again := runDingTalkSource(t, "dingtalk-api.fixture.ts", input)
	exactJSON(t, raw(source), raw(again), "Source replay outputs")
	mu.Lock()
	exactJSON(t, raw(calls), raw(wantCalls), "Source replay physical wire")
	calls = nil
	tokens = 0
	mu.Unlock()
	base, _ := url.Parse(peer.URL)
	var clock int64 = start
	api, e := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}, Now: func() time.Time { return time.UnixMilli(clock) }, Random: func() float64 { return 0.25 }})
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	got := []protocol.JSONObject{}
	cards := map[string]*DingTalkCard{}
	for _, a := range actions {
		if len(a.Get("now")) > 0 {
			clock = int64(dingTalkNumber(a.Get("now")))
		}
		var v any
		var err error
		switch a.String("kind") {
		case "token":
			v, err = api.AccessToken(context.Background())
		case "text":
			err = api.SendText(context.Background(), "https://private.example/hook", TextJSON(a.Get("text")))
		case "create":
			var c *DingTalkCard
			c, err = api.CreateCard(context.Background(), object(a.Get("target")), object(a.Get("options")))
			cards[a.String("id")] = c
			if c != nil {
				v = c.Snapshot()
			}
		case "stream":
			err = api.StreamCard(context.Background(), cards[a.String("id")], TextJSON(a.Get("text")), false)
			v = cards[a.String("id")].Snapshot()
		case "finish":
			err = api.FinishCard(context.Background(), cards[a.String("id")], TextJSON(a.Get("text")))
			v = cards[a.String("id")].Snapshot()
		}
		if err != nil {
			got = append(got, protocol.NewJSONObject(dingTalkField("error", err.Error())))
		} else {
			got = append(got, protocol.NewJSONObject(dingTalkField("value", v)))
		}
	}
	exactJSON(t, raw(got), source.Get("values"), "API values")
	mu.Lock()
	actual := append([]protocol.JSONObject{}, calls...)
	mu.Unlock()
	exactJSON(t, raw(actual), raw(wantCalls), "actual HTTP request wire")
}
