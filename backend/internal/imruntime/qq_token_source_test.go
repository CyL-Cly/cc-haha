package imruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestQQOriginalTokenSingleflightRefreshAndUntrimmedCacheLookup(t *testing.T) {
	want := qqOriginal(t, "native-qq-token-lifecycle.fixture.ts", map[string]any{})
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	started := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		at := calls
		mu.Unlock()
		if at == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw(qqObject(qqField("access_token", "private-token-"+stringNumber(at)), qqField("expires_in", 7200))))
	}))
	defer peer.Close()
	clock := &qqTestTimers{now: 100000000, timers: map[int]struct {
		d time.Duration
		f func()
	}{}}
	client, e := NewQQClient(context.Background(), QQClientOptions{Credentials: qqObject(qqField("appId", "app"), qqField("appSecret", "private-secret")), HTTP: &http.Client{Transport: qqHTTPRoute{peer}}, Now: func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return time.UnixMilli(clock.now) }, Random: func() float64 { return 0 }, Schedule: clock.schedule})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	results := make([]json.RawMessage, 12)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, e := client.GetAccessToken(context.Background())
			if e != nil {
				t.Error(e)
			}
			results[i] = v
		}(i)
	}
	<-started
	mu.Lock()
	pendingCalls := calls
	mu.Unlock()
	close(release)
	wg.Wait()
	client.StartBackgroundRefresh()
	qqLeafUntil(t, func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		for _, v := range clock.timers {
			if v.d == 6900*time.Second {
				return true
			}
		}
		return false
	})
	first := []int64{6900000}
	clock.mu.Lock()
	clock.now += 6900000
	clock.mu.Unlock()
	clock.fire(t, 6900*time.Second)
	qqLeafUntil(t, func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		for _, v := range clock.timers {
			if v.d == time.Minute {
				return true
			}
		}
		return false
	})
	mu.Lock()
	beforeExpiry := calls
	mu.Unlock()
	client.mu.Lock()
	client.refreshCancel()
	client.mu.Unlock()
	qqLeafUntil(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return client.refreshCancel == nil })
	clock.mu.Lock()
	clock.now = 107200000
	clock.mu.Unlock()
	_, e = client.GetAccessToken(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	afterExpiry := calls
	mu.Unlock()
	client.o.Credentials.Set("appId", raw(" app "))
	client.StartBackgroundRefresh()
	qqLeafUntil(t, func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		for _, v := range clock.timers {
			if v.d == time.Minute {
				return true
			}
		}
		return false
	})
	untrimmed := []int64{60000}
	client.mu.Lock()
	client.refreshCancel()
	client.mu.Unlock()
	qqLeafUntil(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return client.refreshCancel == nil })
	exactJSON(t, raw(qqObject(qqField("pendingCalls", pendingCalls), qqField("values", results), qqField("first", first), qqField("beforeExpiry", beforeExpiry), qqField("afterExpiry", afterExpiry), qqField("untrimmed", untrimmed))), want, "actual original token HTTP/cache/refresh")
}
