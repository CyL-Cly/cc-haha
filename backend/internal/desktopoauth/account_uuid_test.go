package desktopoauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func profileTokens() protocol.JSONObject {
	return object([]byte(`{"accessToken":"fake","refreshToken":null,"expiresAt":null,"scopes":["user:inference","user:profile"]}`))
}

func TestAccountUUIDPhysicalProfileIdentityScopeAndCache(t *testing.T) {
	h := host(t)
	s, _, _, _ := fixture(t, h)
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "GET" || r.URL.Path != "/api/oauth/profile" || len(body) != 0 || r.Header.Get("Authorization") != "Bearer fake" || r.Header.Get("User-Agent") != "claude-code/2.1.288" || r.Header.Get("Cache-Control") != "no-cache" || r.Header.Get("Accept") != "application/json, text/plain, */*" {
			t.Errorf("profile wire mismatch: %s %s %v %q", r.Method, r.URL.Path, r.Header, body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"account":{"uuid":"account-a"}}`)
	}))
	defer peer.Close()
	s.options.Config = func() (Config, error) { return Config{BaseAPIURL: peer.URL}, nil }
	saveFixture(t, s, encode(record(field("accessToken", "fake"), field("scopes", []string{"user:inference"}))))
	if value, err := s.GetAccountUUID(context.Background(), "fake"); err != nil || value != nil || calls.Load() != 0 {
		t.Fatal("inference-only token fetched profile", value, err, calls.Load())
	}
	saveFixture(t, s, encode(profileTokens()))
	for i := 0; i < 2; i++ {
		value, err := s.GetAccountUUID(context.Background(), "fake")
		if err != nil || value == nil || *value != "account-a" {
			t.Fatal(value, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("cached account refetched", calls.Load())
	}
	if err := s.DeleteTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	saveFixture(t, s, encode(profileTokens()))
	if value, err := s.GetAccountUUID(context.Background(), "fake"); err != nil || value == nil || calls.Load() != 2 {
		t.Fatal("logout retained old cache", value, err, calls.Load())
	}
}

func TestAccountUUIDTimeoutBackoffLateWarmAndLogoutGeneration(t *testing.T) {
	h := host(t)
	s, _, _, _ := fixture(t, h)
	saveFixture(t, s, encode(profileTokens()))
	var now atomic.Int64
	now.Store(1700000000000)
	s.options.Now = func() time.Time { return time.UnixMilli(now.Load()) }
	s.options.AccountLookupWait = 20 * time.Millisecond
	var calls atomic.Int32
	release := make(chan struct{})
	s.options.FetchAccountUUID = func(ctx context.Context, _ string) (string, error) {
		calls.Add(1)
		select {
		case <-release:
			return "late-account", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	start := time.Now()
	value, err := s.GetAccountUUID(context.Background(), "fake")
	if err != nil || value != nil || time.Since(start) > time.Second {
		t.Fatal("lookup blocked session start", value, err, time.Since(start))
	}
	if value, err = s.GetAccountUUID(context.Background(), "fake"); err != nil || value != nil || calls.Load() != 1 {
		t.Fatal("failed lookup ignored backoff", value, err, calls.Load())
	}
	close(release)
	s.accountWorkers.Wait()
	if value, err = s.GetAccountUUID(context.Background(), "fake"); err != nil || value == nil || *value != "late-account" {
		t.Fatal("late lookup did not warm", value, err)
	}
	s.forgetAccountUUID()
	release = make(chan struct{})
	if value, err = s.GetAccountUUID(context.Background(), "fake"); err != nil || value != nil {
		t.Fatal(value, err)
	}
	if err = s.DeleteTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	s.accountWorkers.Wait()
	s.mu.Lock()
	cached := s.accountUUID
	s.mu.Unlock()
	if cached != "" {
		t.Fatal("late previous-account lookup survived logout", cached)
	}
}

func TestAccountUUIDFailureRetryAndCloseJoin(t *testing.T) {
	h := host(t)
	s, _, _, _ := fixture(t, h)
	saveFixture(t, s, encode(profileTokens()))
	var now atomic.Int64
	now.Store(1700000000000)
	s.options.Now = func() time.Time { return time.UnixMilli(now.Load()) }
	var calls atomic.Int32
	s.options.FetchAccountUUID = func(context.Context, string) (string, error) {
		calls.Add(1)
		return "", fmt.Errorf("profile unavailable")
	}
	if value, err := s.GetAccountUUID(context.Background(), "fake"); value != nil || err != nil {
		t.Fatal(value, err)
	}
	if value, err := s.GetAccountUUID(context.Background(), "fake"); value != nil || err != nil || calls.Load() != 1 {
		t.Fatal(value, err, calls.Load())
	}
	now.Add(60_000)
	if value, err := s.GetAccountUUID(context.Background(), "fake"); value != nil || err != nil || calls.Load() != 2 {
		t.Fatal(value, err, calls.Load())
	}
	s.forgetAccountUUID()
	s.options.AccountLookupWait = time.Millisecond
	started := make(chan struct{})
	exited := make(chan struct{})
	s.options.FetchAccountUUID = func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return "", ctx.Err()
	}
	_, _ = s.GetAccountUUID(context.Background(), "fake")
	<-started
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned before owned profile transport joined")
	}
	if value, err := s.GetAccountUUID(context.Background(), "fake"); value != nil || err != nil {
		t.Fatal("closed service refetched", value, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GetAccountUUID(ctx, "fake"); err == nil {
		t.Fatal("canceled caller admitted")
	}
}
