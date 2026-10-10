package imruntime

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualFeishuSourceCardTimeoutKeepsSubmittedHTTPAndNativeOwnerCloseCancels(t *testing.T) {
	for _, closeOwner := range []bool{false, true} {
		name := "source-timeout-release"
		if closeOwner {
			name = "native-owner-close"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			started, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var startOnce, releaseOnce, completeOnce sync.Once
			active := false
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
				case "/open-apis/cardkit/v1/cards/card-held/elements/streaming_content/content":
					mu.Lock()
					active = true
					calls++
					mu.Unlock()
					startOnce.Do(func() { close(started) })
					select {
					case <-release:
						_, _ = w.Write([]byte(`{"code":0}`))
					case <-r.Context().Done():
					}
					mu.Lock()
					active = false
					mu.Unlock()
					completeOnce.Do(func() { close(completed) })
				case "/control/snapshot":
					mu.Lock()
					data := raw(map[string]any{"active": active, "calls": calls})
					mu.Unlock()
					_, _ = w.Write(data)
				case "/control/release":
					releaseOnce.Do(func() { close(release) })
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unowned path %s", r.URL.Path)
				}
			}))
			defer peer.Close()
			defer releaseOnce.Do(func() { close(release) })
			if !closeOwner {
				want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "timeout", "peer": peer.URL, "timeout": 60}, t.TempDir())
				// Use an independent physical peer below to avoid resetting channels that
				// a Source request could still be unwinding on.
				compareFeishuCardTimeout(t, want)
				return
			}
			c := feishuSourceClient(t, peer, time.Now, "feishu")
			c.o.Environment = map[string]string{"CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS": "60"}
			_, e := c.cardRequest(context.Background(), "cardElement.content", func(ctx context.Context) (json.RawMessage, error) {
				return c.Request(ctx, "PUT", "/open-apis/cardkit/v1/cards/card-held/elements/streaming_content/content", emptyFeishuParams(), json.RawMessage(`{"content":"submitted","sequence":2}`))
			})
			if e == nil || e.Error() != "cardElement.content timed out after 60ms" {
				t.Fatalf("timeout: %v", e)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("submitted request missing")
			}
			mu.Lock()
			isActive, n := active, calls
			mu.Unlock()
			if !isActive || n != 1 {
				t.Fatal("timeout aborted submitted SDK-equivalent request")
			}
			c.Close()
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("owner close did not cancel physical request")
			}
			mu.Lock()
			isActive, n = active, calls
			mu.Unlock()
			if isActive || n != 1 {
				t.Fatal("owner close failed physical cleanup")
			}
		})
	}
}
func emptyFeishuParams() protocol.JSONObject { return protocol.NewJSONObject() }
func compareFeishuCardTimeout(t *testing.T, want json.RawMessage) {
	var mu sync.Mutex
	release, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	active := false
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
			return
		}
		mu.Lock()
		active = true
		calls++
		mu.Unlock()
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"code":0}`))
		case <-r.Context().Done():
		}
		mu.Lock()
		active = false
		mu.Unlock()
		close(done)
	}))
	defer peer.Close()
	defer once.Do(func() { close(release) })
	c := feishuSourceClient(t, peer, time.Now, "feishu")
	c.o.Environment = map[string]string{"CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS": "60"}
	settled := make(chan struct{})
	_, e := c.cardRequest(context.Background(), "cardElement.content", func(ctx context.Context) (json.RawMessage, error) {
		defer close(settled)
		return c.Request(ctx, "PUT", "/open-apis/cardkit/v1/cards/card-held/elements/streaming_content/content", emptyFeishuParams(), json.RawMessage(`{"content":"submitted","sequence":2}`))
	})
	if e == nil {
		t.Fatal("expected original Promise.race timeout")
	}
	mu.Lock()
	before := map[string]any{"active": active, "calls": calls}
	mu.Unlock()
	once.Do(func() { close(release) })
	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Fatal("submitted request could not finish after timeout")
	}
	<-done
	exactJSON(t, raw(map[string]any{"error": e.Error(), "before": before, "completed": true}), want, "actual Source timeout waiter leaves submitted HTTP alive")
}
func TestActualFeishuSourceStreamingFlushCollisionReflushUsesLatestAccumulation(t *testing.T) {
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		packets := []any{}
		streamCount := 0
		started, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := io.ReadAll(r.Body)
			if strings.HasPrefix(r.URL.Path, "/control/") {
				switch r.URL.Path {
				case "/control/started":
					select {
					case <-started:
						_, _ = w.Write([]byte(`{}`))
					case <-r.Context().Done():
					}
				case "/control/snapshot":
					mu.Lock()
					data := raw(map[string]int{"streamCalls": streamCount})
					mu.Unlock()
					_, _ = w.Write(data)
				case "/control/release":
					once.Do(func() { close(release) })
					_, _ = w.Write([]byte(`{}`))
				}
				return
			}
			mu.Lock()
			packets = append(packets, map[string]any{"path": r.URL.RequestURI(), "method": r.Method, "body": json.RawMessage(p)})
			mu.Unlock()
			switch r.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
			case "/open-apis/cardkit/v1/cards":
				_, _ = w.Write([]byte(`{"code":0,"data":{"card_id":"card-private"}}`))
			case "/open-apis/im/v1/messages":
				_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"message-private"}}`))
			case "/open-apis/cardkit/v1/cards/card-private/elements/streaming_content/content":
				mu.Lock()
				streamCount++
				n := streamCount
				mu.Unlock()
				if n == 1 {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				_, _ = w.Write([]byte(`{"code":0}`))
			default:
				_, _ = w.Write([]byte(`{"code":0}`))
			}
		}))
		defer peer.Close()
		defer once.Do(func() { close(release) })
		var result json.RawMessage
		if source {
			result = runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "collision", "peer": peer.URL, "now": int64(1770000000000)}, t.TempDir())
		} else {
			clock := int64(1770000000000)
			var timerMu sync.Mutex
			timers := map[int]func(){}
			next := 0
			schedule := func(_ time.Duration, fn func()) func() {
				timerMu.Lock()
				next++
				id := next
				timers[id] = fn
				timerMu.Unlock()
				return func() { timerMu.Lock(); delete(timers, id); timerMu.Unlock() }
			}
			fire := func() {
				timerMu.Lock()
				ids := []int{}
				for id := range timers {
					ids = append(ids, id)
				}
				sort.Ints(ids)
				timerMu.Unlock()
				for _, id := range ids {
					timerMu.Lock()
					fn := timers[id]
					delete(timers, id)
					timerMu.Unlock()
					if fn != nil {
						fn()
					}
				}
			}
			c := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(clock) }, "feishu")
			card := NewFeishuStreamingCard(context.Background(), FeishuStreamingOptions{Client: c, ChatID: NewText("chat-fixture"), UUID: func() string { return "fixture-stream-uuid" }, Now: func() time.Time { return time.UnixMilli(clock) }, Schedule: schedule})
			if e := card.EnsureCreated(context.Background()); e != nil {
				t.Fatal(e)
			}
			card.AppendText(NewText("first"))
			card.flushMu.Lock()
			card.cancelPendingLocked()
			card.flushMu.Unlock()
			first := make(chan struct{})
			go func() { _ = card.Flush(context.Background()); close(first) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("first actual flush did not start")
			}
			card.AppendText(NewText(" second"))
			clock += 100
			fire()
			mu.Lock()
			during := map[string]int{"streamCalls": streamCount}
			mu.Unlock()
			once.Do(func() { close(release) })
			select {
			case <-first:
			case <-time.After(time.Second):
				t.Fatal("first actual flush did not complete")
			}
			fire()
			_ = card.WaitForFlush(context.Background())
			_ = card.Finalize(context.Background())
			s := card.Snapshot()
			result = raw(map[string]any{"during": during, "phase": s.Phase, "sequence": s.Sequence, "text": s.Text})
			card.Close()
			c.Close()
		}
		mu.Lock()
		receipt := raw(packets)
		mu.Unlock()
		return result, receipt
	}
	want, wantPackets := run(true)
	got, gotPackets := run(false)
	exactJSON(t, got, want, "actual Source flush collision state")
	exactJSON(t, gotPackets, wantPackets, "actual Source first submitted and latest reflush/final HTTP packets")
}
