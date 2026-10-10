package imruntime

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestDingTalkOriginalCardQPSRetryErrorAndFinishFailure(t *testing.T) {
	run := func(source bool) ([]protocol.JSONObject, []protocol.JSONObject) {
		var mu sync.Mutex
		calls := []protocol.JSONObject{}
		statusCalls, streamCalls := 0, 0
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			calls = append(calls, protocol.NewJSONObject(dingTalkField("method", r.Method), dingTalkField("path", r.URL.Path), dingTalkField("body", NewText(string(data)))))
			if r.Method == "PUT" && r.URL.Path == "/v1.0/card/instances" {
				statusCalls++
			}
			if r.URL.Path == "/v1.0/card/streaming" {
				streamCalls++
			}
			s, n := statusCalls, streamCalls
			mu.Unlock()
			if r.URL.Path == "/v1.0/oauth2/accessToken" {
				_, _ = w.Write([]byte(`{"accessToken":"private","expireIn":7200}`))
				return
			}
			if r.Method == "PUT" && r.URL.Path == "/v1.0/card/instances" && s == 1 {
				w.WriteHeader(403)
				_, _ = w.Write([]byte("QpsLimit physical"))
				return
			}
			if r.URL.Path == "/v1.0/card/streaming" && n == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("stream denied"))
				return
			}
			if r.Method == "PUT" && r.URL.Path == "/v1.0/card/instances" && s == 3 {
				w.WriteHeader(500)
				_, _ = w.Write([]byte("finish failed"))
				return
			}
			_, _ = w.Write([]byte("{}"))
		}))
		defer peer.Close()
		credentials := object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))
		actions := []any{map[string]any{"kind": "create", "id": "c", "target": map[string]any{"type": "user", "userId": "U"}, "options": map[string]any{}}, map[string]any{"kind": "stream", "id": "c", "text": "partial"}, map[string]any{"kind": "finish", "id": "c", "text": "final"}}
		values := []protocol.JSONObject{}
		if source {
			out := runDingTalkSource(t, "dingtalk-api.fixture.ts", map[string]any{"base": peer.URL, "now": 1770000000123, "credentials": credentials, "actions": actions})
			values = collectJSONArray(out.Get("values"))
		} else {
			base, _ := url.Parse(peer.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}, Now: func() time.Time { return time.UnixMilli(1770000000123) }, Random: func() float64 { return 0.25 }})
			defer api.Close()
			// Original rate limiter needs Date.now to advance after its wait. Keep card
			// identifiers frozen, while this actual producer's limiter reads wall time.
			api.cards = NewDingTalkCardLimiter(time.Now)
			card, e := api.CreateCard(context.Background(), object(raw(map[string]any{"type": "user", "userId": "U"})), protocol.NewJSONObject())
			if e != nil {
				t.Fatal(e)
			}
			values = append(values, wecomCaptured(card.Snapshot(), nil))
			start := time.Now()
			e = api.StreamCard(context.Background(), card, NewText("partial"), false)
			if time.Since(start) < 2*time.Second {
				t.Fatal("physical QpsLimit backoff was skipped")
			}
			values = append(values, wecomCaptured(nil, e))
			e = api.FinishCard(context.Background(), card, NewText("final"))
			values = append(values, wecomCaptured(card.Snapshot(), e))
		}
		mu.Lock()
		defer mu.Unlock()
		return values, append([]protocol.JSONObject{}, calls...)
	}
	want, wires := run(true)
	again, againWire := run(true)
	exactJSON(t, raw(again), raw(want), "Source QPS result replay")
	exactJSON(t, raw(againWire), raw(wires), "Source QPS wire replay")
	got, gotWire := run(false)
	exactJSON(t, raw(got), raw(want), "original QPS/error/final semantics")
	exactJSON(t, raw(gotWire), raw(wires), "original physical QPS retry requests")
}
func TestDingTalkCardHeaderSuccessTimeoutAndCloseJoin(t *testing.T) {
	for _, mode := range []string{"timeout", "headers", "close"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			gone := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(gone)
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				if mode == "headers" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer peer.Close()
			base, _ := url.Parse(peer.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}, Environment: map[string]string{"CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS": "40"}})
			defer api.Close()
			done := make(chan error, 1)
			go func() {
				done <- api.cardRequest(context.Background(), "POST", "/v1.0/card/instances", raw("private"), protocol.NewJSONObject())
			}()
			<-entered
			if mode == "timeout" {
				select {
				case e := <-done:
					if e == nil || e.Error() != "POST /v1.0/card/instances timed out after 40ms" {
						t.Fatalf("timeout %v", e)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("timer didn't cancel")
				}
			} else if mode == "headers" {
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("success waited body")
				}
				select {
				case <-gone:
					t.Fatal("headers success canceled live body")
				case <-time.After(100 * time.Millisecond):
				}
				api.Close()
			} else {
				api.Close()
				if e := <-done; !errors.Is(e, context.Canceled) {
					t.Fatalf("Close didn't cancel request: %v", e)
				}
			}
			select {
			case <-gone:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP worker escaped Close/timeout")
			}
		})
	}
}
func TestDingTalkCloseJoinsQueuedCardBackoffAndCallback(t *testing.T) {
	api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))})
	api.cards.Backoff()
	done := make(chan error, 1)
	go func() {
		done <- api.cardLimited(context.Background(), "PUT", "/unused", raw("private"), protocol.NewJSONObject())
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		api.cards.mu.Lock()
		entered := api.cards.tail != nil
		api.cards.mu.Unlock()
		if entered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("card admission not observed")
		}
		time.Sleep(time.Millisecond)
	}
	api.Close()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("card wait cancellation %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("card queue escaped Close")
	}
}
func TestDingTalkCloseJoinsInitialGatewayAndActualAsyncCallback(t *testing.T) {
	entered, gone := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		defer close(gone)
		<-r.Context().Done()
	}))
	defer peer.Close()
	base, _ := url.Parse(peer.URL)
	api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"})), HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
	defer api.Close()
	client, _ := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api})
	finished := make(chan error, 1)
	go func() { finished <- client.Connect(context.Background()) }()
	<-entered
	client.Close()
	if e := <-finished; !errors.Is(e, context.Canceled) {
		t.Fatalf("initial gateway not canceled: %v", e)
	}
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway escaped stream Close")
	}
	api2, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))})
	defer api2.Close()
	other, _ := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api2})
	active, complete := make(chan struct{}), make(chan struct{})
	if e := other.schedule(func(ctx context.Context) error { close(active); <-ctx.Done(); close(complete); return ctx.Err() }); e != nil {
		t.Fatal(e)
	}
	<-active
	other.Close()
	select {
	case <-complete:
	default:
		t.Fatal("callback escaped Close")
	}
}
