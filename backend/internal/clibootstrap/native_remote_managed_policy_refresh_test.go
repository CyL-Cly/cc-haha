package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestRemoteManagedPolicyActualExpiredOAuthRefreshThenSettings(t *testing.T) {
	credentials := map[string]any{"future": map[string]any{"preserve": true}, "claudeAiOauth": map[string]any{"accessToken": "fake-old", "refreshToken": "fake-refresh", "expiresAt": 17, "scopes": []string{"user:inference", "user:mcp_servers"}, "subscriptionType": "team", "rateLimitTier": "owned-tier"}}
	var mu sync.Mutex
	requests := []map[string]any{}
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var value any
		if len(body) != 0 {
			if err := json.Unmarshal(body, &value); err != nil {
				t.Error(err)
			}
		}
		mu.Lock()
		requests = append(requests, map[string]any{"method": r.Method, "path": r.URL.Path, "auth": r.Header.Get("Authorization"), "beta": r.Header.Get("anthropic-beta"), "body": value})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/oauth/token":
			_, _ = io.WriteString(w, `{"access_token":"fake-new","refresh_token":"fake-refresh-new","expires_in":3600,"scope":"user:inference user:mcp_servers"}`)
		case "GET /api/oauth/profile":
			w.WriteHeader(403)
		case "GET /api/claude_code/settings":
			if r.Header.Get("Authorization") != "Bearer fake-new" {
				t.Error("stale token used for managed settings")
			}
			_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"model":"opus"}}`)
		default:
			t.Error("unexpected HTTP", r.Method, r.URL.Path)
			w.WriteHeader(403)
		}
	}))
	env := map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca, "CLAUDE_CODE_OAUTH_TOKEN": ""}
	m, f := managedRemoteMainWithCredentials(t, env, nil, credentials)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, m.remoteManaged.wait); err != nil {
		t.Fatal(err)
	}
	waitRemoteManagedTerminal(t, ctx, m.remoteManaged)
	if policy := m.remoteManaged.snapshot(); policy == nil || policy.String("model") != "opus" {
		t.Fatal("refresh failed open instead of closing expired credential path", policy)
	}
	raw, err := os.ReadFile(filepath.Join(f.config, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var actualCredentials any
	if err = json.Unmarshal(raw, &actualCredentials); err != nil {
		t.Fatal(err)
	}
	m.Close()
	mu.Lock()
	goRequests := append([]map[string]any{}, requests...)
	requests = nil
	mu.Unlock()
	sourceRaw := managedSourceJSON(t, env, map[string]any{"credentials": credentials, "fallback": map[string]any{"model": "haiku"}, "includeCredentials": true})
	var source struct {
		After       map[string]any
		Credentials any
	}
	if err = json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal(err, string(sourceRaw))
	}
	mu.Lock()
	sourceRequests := append([]map[string]any{}, requests...)
	mu.Unlock()
	if source.After["model"] != "opus" || !reflect.DeepEqual(goRequests, sourceRequests) || !reflect.DeepEqual(actualCredentials, source.Credentials) {
		t.Fatalf("real Source freshness/settings chain mismatch\nSource=%s HTTP=%s\nGo creds=%s HTTP=%s", sourceRaw, rawMain(sourceRequests), raw, rawMain(goRequests))
	}
	refreshCount := 0
	for _, r := range goRequests {
		if r["path"] == "/v1/oauth/token" {
			refreshCount++
		}
	}
	if refreshCount != 1 {
		t.Fatal("freshness was skipped or repeated", refreshCount)
	}
}

func TestRemoteManagedPolicyCacheUnblocksBeforeHeldBackgroundRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			w.WriteHeader(304)
		case <-r.Context().Done():
		}
	}))
	defer once.Do(func() { close(release) })
	cached := map[string]any{"model": "sonnet"}
	m, _ := managedRemoteMain(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, cached)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.remoteManaged.wait(scope) }); err != nil {
		t.Fatal("warm cache blocked on physical refresh", err)
	}
	if count, _ := m.remoteManaged.refs.Snapshot(); count == 0 {
		t.Fatal("test did not retain real background request")
	}
	once.Do(func() { close(release) })
	waitRemoteManagedTerminal(t, ctx, m.remoteManaged)
}

func TestRemoteManagedPolicyCancelledWaiterDoesNotOwnAdmittedFetch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"model":"opus"}}`)
		case <-r.Context().Done():
		}
	}))
	defer once.Do(func() { close(release) })
	m, _ := managedRemoteMain(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiter, cancelWait := context.WithCancel(ctx)
	waiting := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.Owner.Run(waiter, func(scope *cooperative.Scope) error { close(waiting); return m.remoteManaged.wait(scope) })
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelWait()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiter cancellation identity", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if m.remoteManaged.life.Err() != nil {
		t.Fatal("cancelled waiter killed independent fetch")
	}
	once.Do(func() { close(release) })
	waitRemoteManagedTerminal(t, ctx, m.remoteManaged)
	if err := m.Owner.Run(ctx, m.remoteManaged.wait); err != nil {
		t.Fatal("future waiter inherited observer cancellation", err)
	}
	if value := m.remoteManaged.snapshot(); value == nil || value.String("model") != "opus" {
		t.Fatal("admitted HTTP result was abandoned", value)
	}
}

func TestRemoteManagedPolicyAxiosRedirectCredentialsAndRealHopLimit(t *testing.T) {
	parsed := func(text string) *url.URL {
		value, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := &http.Request{Method: "GET", URL: parsed("https://api.anthropic.com/settings"), Header: http.Header{"Authorization": []string{"Bearer private"}, "Cookie": []string{"owned-cookie"}, "X-Api-Key": []string{"owned-api"}}}
	child := &http.Request{Method: "GET", URL: parsed("https://child.api.anthropic.com/next")}
	if err := remoteManagedRedirect(child, []*http.Request{initial}); err != nil || child.Header.Get("Authorization") != "Bearer private" {
		t.Fatal("same/subdomain redirect lost Source credentials", err)
	}
	normalized := &http.Request{Method: "GET", URL: parsed("https://API.ANTHROPIC.COM:443/next")}
	if err := remoteManagedRedirect(normalized, []*http.Request{initial}); err != nil || normalized.Header.Get("Authorization") != "Bearer private" {
		t.Fatal("WHATWG host/default-port identity changed", normalized.Header, err)
	}
	foreign := &http.Request{Method: "GET", URL: parsed("https://foreign.invalid/next")}
	if err := remoteManagedRedirect(foreign, []*http.Request{initial}); err != nil || foreign.Header.Get("Authorization") != "" || foreign.Header.Get("Cookie") != "" || foreign.Header.Get("x-api-key") != "owned-api" {
		t.Fatal("Source boundary sensitive-header contract", foreign.Header, err)
	}
	back := &http.Request{Method: "GET", URL: parsed("https://api.anthropic.com/back")}
	if err := remoteManagedRedirect(back, []*http.Request{initial, foreign}); err != nil || back.Header.Get("Authorization") != "" {
		t.Fatal("stripped credentials resurrected on later hop", back.Header, err)
	}
	downgrade := &http.Request{Method: "GET", URL: parsed("http://api.anthropic.com/next")}
	if err := remoteManagedRedirect(downgrade, []*http.Request{initial}); err != nil || downgrade.Header.Get("Authorization") != "" {
		t.Fatal("HTTPS downgrade retained credentials", err)
	}
	post := initial.Clone(context.Background())
	post.Method = "POST"
	post.Header.Set("Content-Type", "application/json")
	get := &http.Request{Method: "GET", URL: parsed("https://api.anthropic.com/after-303")}
	if err := remoteManagedRedirect(get, []*http.Request{post}); err != nil || get.Header.Get("Content-Type") != "" {
		t.Fatal("OAuth POST redirect retained entity headers", get.Header, err)
	}
	hops := make([]*http.Request, 21)
	for i := range hops {
		hops[i] = initial
	}
	if err := remoteManagedRedirect(get, hops); err != nil {
		t.Fatal("Axios 21-hop contract shortened", err)
	}
	if err := remoteManagedRedirect(get, append(hops, initial)); err == nil {
		t.Fatal("Axios hop limit lost")
	}
}
