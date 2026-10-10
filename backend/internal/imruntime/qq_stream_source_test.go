package imruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestQQActualSDKStreamConcurrentCompleteCancelThrottleAndRateRetries(t *testing.T) {
	for _, mode := range []string{"concurrent-complete", "concurrent-cancel", "throttle", "cancel-timer", "retry-rate-message", "no-retry-biz-code"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			packets := []protocol.JSONObject{}
			count := 0
			first := make(chan struct{})
			second := make(chan struct{})
			release := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/await-first":
					<-first
					w.WriteHeader(200)
					return
				case "/release-first":
					close(release)
					w.WriteHeader(200)
					return
				case "/await-second":
					<-second
					w.WriteHeader(200)
					return
				}
				body, _ := io.ReadAll(r.Body)
				logical := r.Header.Get("X-Fixture-Logical-URL")
				w.Header().Set("Content-Type", "application/json")
				if logical == QQTokenURL {
					w.Write(raw(qqObject(qqField("access_token", "private-token"), qqField("expires_in", 7200))))
					return
				}
				mu.Lock()
				packets = append(packets, object(body))
				count++
				at := count
				if at == 1 {
					close(first)
				}
				if at == 2 {
					close(second)
				}
				mu.Unlock()
				if at == 1 && strings.HasPrefix(mode, "concurrent") {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if mode == "retry-rate-message" && at <= 3 {
					w.WriteHeader(429)
					w.Write(raw(qqObject(qqField("code", 50002), qqField("message", "private rate limit"))))
					return
				}
				if mode == "no-retry-biz-code" {
					w.WriteHeader(429)
					w.Write(raw(qqObject(qqField("code", 50002), qqField("message", "private too many"))))
					return
				}
				w.Write(raw(qqObject(qqField("id", "private-stream"))))
			}))
			defer peer.Close()
			want := object(qqOriginal(t, "native-qq-stream-lifecycle.fixture.ts", map[string]any{"httpURL": peer.URL, "mode": mode}))
			mu.Lock()
			expected := append([]protocol.JSONObject{}, packets...)
			packets = []protocol.JSONObject{}
			count = 0
			first = make(chan struct{})
			second = make(chan struct{})
			release = make(chan struct{})
			mu.Unlock()
			clock := &qqTestTimers{now: 100000000, timers: map[int]struct {
				d time.Duration
				f func()
			}{}}
			schedule := func(d time.Duration, f func()) func() {
				if d == time.Second || d == 2*time.Second || d == 4*time.Second {
					clock.mu.Lock()
					clock.now += d.Milliseconds()
					clock.mu.Unlock()
					t := time.AfterFunc(0, f)
					return func() { t.Stop() }
				}
				return clock.schedule(d, f)
			}
			client, e := NewQQClient(context.Background(), QQClientOptions{Credentials: qqObject(qqField("appId", "app"), qqField("appSecret", "private-secret")), HTTP: &http.Client{Transport: qqHTTPRoute{peer}}, Now: func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return time.UnixMilli(clock.now) }, Random: func() float64 { return 0 }, Schedule: schedule})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			stream, e := client.OpenStream(context.Background(), QQTarget{"c2c", NewText("private-user"), NewText("private-inbound")}, 600*time.Millisecond)
			if e != nil {
				t.Fatal(e)
			}
			values := []protocol.JSONObject{}
			switch mode {
			case "concurrent-complete", "concurrent-cancel":
				done := make(chan error, 1)
				go func() { done <- stream.Update(NewText("A")) }()
				<-first
				if e = stream.Update(NewText("AB")); e != nil {
					t.Fatal(e)
				}
				if mode == "concurrent-complete" {
					finished := make(chan error, 1)
					go func() { finished <- stream.Complete() }()
					qqLeafUntil(t, func() bool { stream.mu.Lock(); defer stream.mu.Unlock(); return stream.completed })
					close(release)
					if e = <-done; e != nil {
						t.Fatal(e)
					}
					if e = <-finished; e != nil {
						t.Fatal(e)
					}
				} else {
					stream.Cancel()
					close(release)
					if e = <-done; e != nil {
						t.Fatal(e)
					}
					if e = stream.Complete(); e != nil {
						t.Fatal(e)
					}
					stream.Update(NewText("ignored"))
				}
			case "throttle", "cancel-timer":
				if e = stream.Update(NewText("A")); e != nil {
					t.Fatal(e)
				}
				stream.Update(NewText("AB"))
				clock.mu.Lock()
				timers := len(clock.timers)
				clock.mu.Unlock()
				values = append(values, qqObject(qqField("timers", timers)))
				if mode == "cancel-timer" {
					stream.Cancel()
					stream.Complete()
					clock.mu.Lock()
					timers = len(clock.timers)
					clock.mu.Unlock()
					values = append(values, qqObject(qqField("timers", timers)))
				} else {
					clock.mu.Lock()
					clock.now += 600
					clock.mu.Unlock()
					clock.fire(t, 600*time.Millisecond)
					<-second
					if e = stream.Complete(); e != nil {
						t.Fatal(e)
					}
				}
			default:
				e = stream.Update(NewText("A"))
				if e != nil {
					values = append(values, qqObject(qqField("error", e.Error())))
				} else {
					values = append(values, qqObject(qqField("success", true)))
				}
				stream.Cancel()
			}
			mu.Lock()
			actual := raw(packets)
			mu.Unlock()
			exactJSON(t, actual, raw(expected), "actual stream packets")
			exactJSON(t, raw(qqObject(qqField("values", values))), raw(want), "actual stream result/timer/cancel")
		})
	}
}
