package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func catalogFixture(t *testing.T, host string, now *atomic.Int64) *Service {
	t.Helper()
	home := t.TempDir()
	parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now.Load()) }, HTTPClient: &http.Client{Transport: openAITestTransport{host, http.DefaultTransport}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	return parent
}

func TestOpenAICatalogRefreshesActualCredentialsBeforeBackgroundDiscovery(t *testing.T) {
	var now atomic.Int64
	now.Store(1770000000000)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	requests := []string{}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("refresh_token") != "fake-old-refresh" || r.Form.Get("grant_type") != "refresh_token" {
				t.Error("refresh protocol changed")
			}
			_, _ = io.WriteString(w, `{"access_token":"fake-fresh-access","refresh_token":"fake-fresh-refresh","expires_in":3600}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != "0.157.0" || r.Method != "GET" {
			t.Error("original catalog URL/method changed")
		}
		for key, want := range map[string]string{"Accept": "application/json", "Authorization": "Bearer fake-fresh-access", "ChatGPT-Account-Id": "owned-account", "originator": "codex_cli_rs", "User-Agent": "codex-cli/0.157.0"} {
			if r.Header.Get(key) != want {
				t.Errorf("original %s header mismatch", key)
			}
		}
		close(entered)
		defer close(finished)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"models":[{"slug":"account-only","visibility":"list","supported_reasoning_levels":[{"effort":"max"}]}]}`)
	}))
	defer host.Close()
	defer close(release)
	parent := catalogFixture(t, host.URL, &now)
	service := parent.OpenAI()
	if err := service.SaveTokens(context.Background(), object([]byte(`{"accessToken":"fake-old-access","refreshToken":"fake-old-refresh","expiresAt":1,"accountId":"owned-account","future":{"keep":true}}`))); err != nil {
		t.Fatal(err)
	}
	models, err := service.ModelCatalog(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, providerproxy.OpenAIModelCatalog()) {
		t.Fatal("cold discovery did not return bundled immediately")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("physical model catalog did not start")
	}
	doc, err := service.LoadTokens(context.Background())
	if err != nil || doc.Record().String("accessToken") != "fake-fresh-access" {
		t.Fatal("discovery did not persist actual refreshed credential")
	}
	info, err := os.Stat(service.TokenPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("refreshed token lost private mode")
	}
	mu.Lock()
	paths := append([]string{}, requests...)
	mu.Unlock()
	if !reflect.DeepEqual(paths, []string{"/oauth/token", "/backend-api/codex/models"}) {
		t.Fatal("catalog ran before token refresh")
	}
	// Cancel and join the real held HTTP exchange through service shutdown.
	service.Close()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog exchange outlived Close")
	}
	if _, err = service.ModelCatalog(context.Background(), false); err == nil {
		t.Fatal("closed service admitted discovery")
	}
}

func TestOpenAICatalogMissingUnreadableAndFailedRefreshNeverUseCLISlot(t *testing.T) {
	for _, profile := range []string{"logged-out", "unreadable", "expired-no-refresh", "refresh-rejected"} {
		t.Run(profile, func(t *testing.T) {
			var now atomic.Int64
			now.Store(1770000000000)
			var requests atomic.Int64
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/oauth/token" {
					t.Error("unavailable desktop account reached models endpoint")
				}
				w.WriteHeader(401)
			}))
			defer host.Close()
			parent := catalogFixture(t, host.URL, &now)
			service := parent.OpenAI()
			cli := filepath.Join(parent.options.ConfigDir, "unrelated-cli-oauth.json")
			if err := os.MkdirAll(parent.options.ConfigDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cli, []byte(`{"accessToken":"must-not-be-used","expiresAt":9999999999999}`), 0600); err != nil {
				t.Fatal(err)
			}
			if profile != "logged-out" {
				raw := `{"accessToken":"fake-old","expiresAt":1}`
				if profile == "unreadable" {
					raw = "invalid JSON"
				}
				if profile == "refresh-rejected" {
					raw = `{"accessToken":"fake-old","expiresAt":1,"refreshToken":"fake-refresh"}`
				}
				if err := os.MkdirAll(filepath.Dir(service.TokenPath()), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(service.TokenPath(), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			models, err := service.ModelCatalog(context.Background(), true)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(models, providerproxy.OpenAIModelCatalog()) {
				t.Fatal("unavailable account must return Source fallback")
			}
			want := int64(0)
			if profile == "refresh-rejected" {
				want = 1
			}
			if requests.Load() != want {
				t.Fatal("unexpected credential/catalog transport")
			}
			stored, _ := os.ReadFile(cli)
			if !bytes.Contains(stored, []byte("must-not-be-used")) {
				t.Fatal("desktop discovery mutated CLI credential slot")
			}
		})
	}
}

func TestOpenAICatalogFixedFiveSecondTimeoutAndFailureBackoff(t *testing.T) {
	var now atomic.Int64
	now.Store(1770000000000)
	var requests atomic.Int64
	finished := make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		defer close(finished)
		<-r.Context().Done()
	}))
	defer host.Close()
	parent := catalogFixture(t, host.URL, &now)
	service := parent.OpenAI()
	if err := service.SaveTokens(context.Background(), object([]byte(`{"accessToken":"fake-timeout","expiresAt":null}`))); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	models, err := service.ModelCatalog(context.Background(), true)
	elapsed := time.Since(started)
	if err != nil || !reflect.DeepEqual(models, providerproxy.OpenAIModelCatalog()) {
		t.Fatal("forced timeout must return Source fallback")
	}
	if elapsed < 4500*time.Millisecond || elapsed > 7*time.Second {
		t.Fatalf("actual timeout %v differs from original 5 seconds", elapsed)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timed out native HTTP exchange was not closed")
	}
	for i := 0; i < 8; i++ {
		_, err = service.ModelCatalog(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("failed refresh ignored one-minute backoff")
	}
}

func TestOpenAICatalogActualBurstSingleFlightIndependentSnapshotsAndClose(t *testing.T) {
	var now atomic.Int64
	now.Store(1770000000000)
	var requests atomic.Int64
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) != 1 {
			t.Error("burst opened a second catalog request")
		}
		close(entered)
		defer close(closed)
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"models":[{"slug":"remote","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`)
		case <-r.Context().Done():
		}
	}))
	defer host.Close()
	parent := catalogFixture(t, host.URL, &now)
	service := parent.OpenAI()
	if err := service.SaveTokens(context.Background(), object([]byte(`{"accessToken":"fake-burst","expiresAt":null,"accountId":"same"}`))); err != nil {
		t.Fatal(err)
	}
	var callers sync.WaitGroup
	callers.Add(24)
	for i := 0; i < 24; i++ {
		go func() {
			defer callers.Done()
			models, err := service.ModelCatalog(context.Background(), false)
			if err != nil || len(models) != len(providerproxy.OpenAIModelCatalog()) {
				t.Error("burst did not immediately return bundled")
			}
		}()
	}
	callers.Wait()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog refresh not started")
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("physical response did not finish")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.catalog.mu.Lock()
		flight := service.catalog.inFlight
		service.catalog.mu.Unlock()
		if !flight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background cache not settled")
		}
		time.Sleep(time.Millisecond)
	}
	models, err := service.ModelCatalog(context.Background(), false)
	if err != nil || len(models) != 1 || models[0].Value != "remote" {
		t.Fatal("native response did not populate cache")
	}
	models[0].Raw[0] = '!'
	models[0].SupportedReasoningEfforts[0] = "mutated"
	next, err := service.ModelCatalog(context.Background(), false)
	if err != nil || !json.Valid(next[0].Raw) || next[0].SupportedReasoningEfforts[0] != "low" {
		t.Fatal("caller changed owning cache")
	}
	if requests.Load() != 1 {
		t.Fatal("fresh cache reopened upstream")
	}
}

func TestOpenAICatalogInvalidResponsesMatchSourceFallback(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"models":null}`, `{"models":{}}`, `{"models":[]}`, `{"models":[null]}`, `{"models":[{"slug":"hidden","visibility":"hide"}]}`, `not JSON`} {
		t.Run(strings.ReplaceAll(body, "/", "_"), func(t *testing.T) {
			var now atomic.Int64
			now.Store(1770000000000)
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer host.Close()
			parent := catalogFixture(t, host.URL, &now)
			service := parent.OpenAI()
			if err := service.SaveTokens(context.Background(), object([]byte(`{"accessToken":"fake-invalid","expiresAt":null}`))); err != nil {
				t.Fatal(err)
			}
			models, err := service.ModelCatalog(context.Background(), true)
			if err != nil || !reflect.DeepEqual(models, providerproxy.OpenAIModelCatalog()) {
				t.Fatalf("invalid catalog did not fall back: %v", err)
			}
		})
	}
}
