package clibootstrap

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type remoteManagedObservation struct {
	Eligible bool     `json:"eligible"`
	Before   any      `json:"before"`
	After    any      `json:"after"`
	Policy   any      `json:"policy"`
	Changes  []string `json:"changes"`
	Checksum any      `json:"checksum"`
	Disk     any      `json:"disk"`
	Mode     any      `json:"mode"`
}

func managedJSONValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func managedRemoteMain(t *testing.T, env map[string]string, cached any) (*MainPrint, nativePluginFixture) {
	t.Helper()
	return managedRemoteMainWithCredentials(t, env, cached, nil)
}
func managedRemoteMainWithCredentials(t *testing.T, env map[string]string, cached, credentials any) (*MainPrint, nativePluginFixture) {
	t.Helper()
	f := newNativePluginFixture(t)
	delete(f.n.Environment, "ANTHROPIC_BASE_URL")
	f.n.Environment["DISABLE_TELEMETRY"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-owned-env"
	for key, value := range env {
		f.n.Environment[key] = value
	}
	f.n.CCRCredentialDirectory = filepath.Join(f.home, "no-real-remote")
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.write(t, filepath.Join(f.managed, "managed-settings.json"), `{"model":"haiku"}`)
	if cached != nil {
		f.write(t, filepath.Join(f.config, "remote-settings.json"), string(rawMain(cached)))
	}
	if credentials != nil {
		f.write(t, filepath.Join(f.config, ".credentials.json"), string(rawMain(credentials)))
	}
	f.n.NowMillis = func() float64 { return 1000000 }
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, Platform: "darwin", PID: os.Getpid(), Version: "2.1.288", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Sources: DefaultState(), SystemPrompt: "private remote policy consumer", Tools: []string{""}, MaxTurns: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, f
}
func managedSourceCapture(t *testing.T, env map[string]string, input map[string]any) remoteManagedObservation {
	t.Helper()
	out := managedSourceJSON(t, env, input)
	var result remoteManagedObservation
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err, string(out))
	}
	return result
}

func TestRemoteManagedPolicyActualSourceCacheHTTPAndHotReload(t *testing.T) {
	for _, kind := range []string{"cold200", "warm200", "warm304", "empty204", "empty404", "denied401", "denied403", "invalid-envelope", "invalid-settings", "six503", "redirect-gzip", "clear", "refresh", "custom-provider", "local-agent", "skip", "api-key"} {
		t.Run(kind, func(t *testing.T) {
			var cached any
			if kind != "cold200" {
				cached = map[string]any{"model": "sonnet", "env": map[string]any{"CACHE_MARKER": "old"}}
			}
			received := []map[string]string{}
			var mu sync.Mutex
			attempt := 0
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/claude_code/settings") {
					t.Error("unexpected non-settings HTTP", r.Method, r.URL.Path)
					w.WriteHeader(403)
					return
				}
				mu.Lock()
				attempt++
				number := attempt
				received = append(received, map[string]string{"path": r.URL.RequestURI(), "apiKey": r.Header.Get("x-api-key"), "authorization": r.Header.Get("Authorization"), "beta": r.Header.Get("anthropic-beta"), "agent": r.Header.Get("User-Agent"), "etag": r.Header.Get("If-None-Match"), "encoding": r.Header.Get("Accept-Encoding")})
				mu.Unlock()
				status := 200
				switch kind {
				case "warm304":
					status = 304
				case "empty204":
					status = 204
				case "empty404":
					status = 404
				case "denied401":
					status = 401
				case "denied403":
					status = 403
				case "six503":
					status = 503
				}
				if kind == "redirect-gzip" && r.URL.RawQuery == "" {
					w.Header().Set("Location", "/api/claude_code/settings?redirected=1")
					w.WriteHeader(302)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if status != 200 {
					w.WriteHeader(status)
					return
				}
				if kind == "invalid-envelope" && number == 1 {
					_, _ = io.WriteString(w, `{"uuid":1,"checksum":"x","settings":{}}`)
					return
				}
				if kind == "invalid-settings" && number == 1 {
					_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"model":77}}`)
					return
				}
				payload := []byte(`{"uuid":"owned","checksum":"server-value-is-not-cache-etag","settings":{"model":"opus","env":{"REMOTE_MARKER":"fresh"}}}`)
				if kind == "redirect-gzip" {
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = writer.Write(payload)
					_ = writer.Close()
					return
				}
				_, _ = w.Write(payload)
			}))
			env := map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}
			switch kind {
			case "custom-provider":
				env["ANTHROPIC_BASE_URL"] = "https://custom.invalid"
			case "local-agent":
				env["CLAUDE_CODE_ENTRYPOINT"] = "local-agent"
			case "skip":
				env["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
			case "api-key":
				env["ANTHROPIC_API_KEY"] = "fake-owned-api"
				env["CLAUDE_CODE_OAUTH_TOKEN"] = ""
			}
			m, f := managedRemoteMain(t, env, cached)
			s := m.remoteManaged
			goResult := remoteManagedObservation{Eligible: s.eligible != nil && *s.eligible, Changes: []string{}}
			if before := s.snapshot(); before != nil {
				goResult.Before = managedJSONValue(t, before)
			}
			// Actual cache-invalidation consumer is retained, with an independent event
			// record after it. No HTTP completion order is precomputed by the fixture.
			originalNotify := s.notify
			s.notify = func(scope *cooperative.Scope) error {
				if err := originalNotify(scope); err != nil {
					return err
				}
				goResult.Changes = append(goResult.Changes, "policySettings")
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			action := "load"
			if kind == "clear" || kind == "refresh" {
				action = kind
			}
			if action == "load" {
				if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
					t.Fatal(err)
				}
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return s.wait(scope) }); err != nil {
					t.Fatal(err)
				}
			} else if action == "clear" {
				if err := m.Owner.Run(ctx, s.clear); err != nil {
					t.Fatal(err)
				}
			} else {
				// refresh has no loading Promise. The real notify is the completion marker.
				done := make(chan struct{})
				notify := s.notify
				s.notify = func(scope *cooperative.Scope) error { err := notify(scope); close(done); return err }
				if err := m.RefreshRemoteManagedPolicy(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			waitRemoteManagedTerminal(t, ctx, s)
			if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
				if after := s.snapshot(); after != nil {
					raw, err := after.MarshalJSON()
					if err != nil {
						return err
					}
					if err = json.Unmarshal(raw, &goResult.After); err != nil {
						return err
					}
				}
				value, err := m.Startup.GetSettingsForSource("policySettings")
				if err != nil {
					return err
				}
				if value != nil {
					raw, err := value.MarshalJSON()
					if err != nil {
						return err
					}
					return json.Unmarshal(raw, &goResult.Policy)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.config, "remote-settings.json")
			if raw, err := os.ReadFile(path); err == nil {
				if err = json.Unmarshal(raw, &goResult.Disk); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				goResult.Mode = float64(info.Mode().Perm())
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			m.Close() // physical writes/HTTP are terminal before Source begins.
			mu.Lock()
			goRequests := append([]map[string]string{}, received...)
			received = nil
			attempt = 0
			mu.Unlock()
			input := map[string]any{"action": action, "fallback": map[string]any{"model": "haiku"}}
			if cached != nil {
				input["cached"] = cached
			}
			source := managedSourceCapture(t, env, input)
			mu.Lock()
			sourceRequests := append([]map[string]string{}, received...)
			mu.Unlock()
			if !reflect.DeepEqual(goResult, source) {
				t.Fatalf("complete Source observation mismatch\nSource=%s\nGo=%s", rawMain(source), rawMain(goResult))
			}
			if !reflect.DeepEqual(goRequests, sourceRequests) {
				t.Fatalf("actual request contract mismatch\nSource=%s\nGo=%s", rawMain(sourceRequests), rawMain(goRequests))
			}
			expected := 1
			switch kind {
			case "invalid-envelope", "invalid-settings", "redirect-gzip":
				expected = 2
			case "six503":
				expected = 6
			case "clear", "custom-provider", "local-agent", "skip":
				expected = 0
			}
			if len(goRequests) != expected {
				t.Fatalf("actual retries/redirects changed: %d != %d", len(goRequests), expected)
			}
		})
	}
}

func TestRemoteManagedPolicyOAuthEligibilityAndFetchOrder(t *testing.T) {
	eligible := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: quoteMain("owned")}, protocol.JSONField{Name: "subscriptionType", Value: []byte("null")})
	for _, eligibilityOnly := range []bool{true, false} {
		calls := []string{}
		c, err := sampleRemoteManagedCredentials(eligibilityOnly, func() (*protocol.JSONObject, error) { calls = append(calls, "oauth"); return &eligible, nil }, func() (string, error) { calls = append(calls, "api-fd"); return "owned-api", nil })
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"oauth"}
		if !eligibilityOnly {
			expected = []string{"api-fd"}
		}
		if !reflect.DeepEqual(calls, expected) || !remoteManagedCredentialEligible(c) {
			t.Fatal("eligibility/API-first consumer order", calls, c)
		}
	}
	for _, subscription := range []string{"pro", "team", "enterprise", ""} {
		for _, inference := range []bool{false, true} {
			tokens := eligible.Clone()
			tokens.Set("subscriptionType", quoteMain(subscription))
			scopes := []string{}
			if inference {
				scopes = append(scopes, "user:inference")
			}
			tokens.Set("scopes", rawMain(scopes))
			want := (subscription == "team" || subscription == "enterprise") && inference
			if remoteManagedOAuthEligible(&tokens) != want {
				t.Fatal("real subscription/scope gate", subscription, inference)
			}
		}
	}
	for _, row := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, true}, {map[string]string{"ANTHROPIC_BASE_URL": "https://API.ANTHROPIC.COM:443"}, true}, {map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:444"}, false}, {map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com.evil.invalid"}, false}, {map[string]string{"ANTHROPIC_BASE_URL": "https://api-staging.anthropic.com", "USER_TYPE": "ant"}, true}, {map[string]string{"CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}, false}, {map[string]string{"CLAUDE_CODE_ENTRYPOINT": "local-agent"}, false}, {map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, false},
	} {
		if remoteManagedProviderEligible(row.env) != row.want {
			t.Fatal("actual provider/base-url gate", row.env)
		}
	}
}

func TestRemoteManagedPolicyCloseJoinsHeldRealHTTPAndNoLateCache(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
	m, f := managedRemoteMain(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, nil)
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
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("close did not join physical request", ctx.Err())
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("owned TLS request not cancelled", ctx.Err())
	}
	if _, err := os.Stat(filepath.Join(f.config, "remote-settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("close allowed a late cache write", err)
	}
	if count, _ := m.remoteManaged.refs.Snapshot(); count != 0 {
		t.Fatal("live remote native references", count)
	}
	if err := m.Owner.Run(context.Background(), m.remoteManaged.begin); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed initialization was re-admitted", err)
	}
}

func TestRemoteManagedPolicyOwnerForeignRetainedAndCallerCancel(t *testing.T) {
	m, _ := managedRemoteMain(t, map[string]string{"CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}, nil)
	var retained *cooperative.Scope
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { retained = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.remoteManaged.check(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil authority", err)
	}
	if err := m.remoteManaged.check(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained authority", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.remoteManaged.check(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign authority: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Owner.Run(ctx, m.remoteManaged.begin); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller was admitted", err)
	}
	if m.remoteManaged.life.Err() != nil {
		t.Fatal("one caller poisoned independent policy lifetime")
	}
}

func waitRemoteManagedTerminal(t *testing.T, ctx context.Context, s *nativeRemoteManagedPolicy) {
	t.Helper()
	if count, idle := s.refs.Snapshot(); count != 0 {
		select {
		case <-idle:
		case <-ctx.Done():
			t.Fatal("actual remote native work did not finish", ctx.Err())
		}
	}
}
