package desktopoauth

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type openAITestTransport struct {
	address string
	base    http.RoundTripper
}

func TestDesktopOpenAICallbackRejectExpiryAndCloseCancelsActualExchange(t *testing.T) {
	for _, profile := range []string{"missing-code", "invalid-state", "expiry", "active-exchange-close"} {
		t.Run(profile, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			exchangeFinished := make(chan struct{}, 1)
			tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				entered <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
					t.Error("owned token exchange was not cancelled")
				}
				exchangeFinished <- struct{}{}
			}))
			defer tokens.Close()
			home := t.TempDir()
			var now atomic.Int64
			now.Store(1770000000000)
			parent, e := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now.Load()) }, HTTPClient: &http.Client{Transport: openAITestTransport{tokens.URL, http.DefaultTransport}}})
			if e != nil {
				t.Fatal(e)
			}
			defer parent.Close()
			port := 0
			service := NewOpenAIService(parent, &port)
			defer service.Close()
			session, e := service.StartSession(1)
			if e != nil {
				t.Fatal(e)
			}
			if profile == "expiry" {
				now.Add(300000)
				if service.GetSession(session.State) == nil {
					t.Fatal("Source exact TTL boundary changed")
				}
				now.Add(1)
				if service.GetSession(session.State) != nil {
					t.Fatal("expired state remained")
				}
			} else if profile == "active-exchange-close" {
				requestDone := make(chan struct{})
				go func() {
					defer close(requestDone)
					response, err := http.Get(session.RedirectURI + "?code=private-fixture&state=" + session.State)
					if err == nil {
						_, _ = io.Copy(io.Discard, response.Body)
						_ = response.Body.Close()
					}
				}()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("actual token exchange did not start")
				}
				service.Close()
				select {
				case <-exchangeFinished:
				case <-time.After(2 * time.Second):
					t.Fatal("token connection outlived service")
				}
				select {
				case <-requestDone:
				case <-time.After(2 * time.Second):
					t.Fatal("callback request outlived service")
				}
			} else {
				query := "?state=" + session.State
				want := "Authorization code not found"
				if profile == "invalid-state" {
					query = "?code=fixture&state=foreign"
					want = "Invalid state parameter"
				}
				response, e := http.Get(session.RedirectURI + query)
				if e != nil {
					t.Fatal(e)
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if response.StatusCode != 400 || string(body) != want || response.Header.Get("Content-Type") != "" {
					t.Fatalf("Source callback failure %d %s %v", response.StatusCode, body, response.Header)
				}
				service.Dispose()
			}
			if service.GetSession(session.State) != nil {
				t.Fatal("finished state remains")
			}
			if _, e = os.Stat(service.TokenPath()); !os.IsNotExist(e) {
				t.Fatal("failed/cancelled callback wrote credentials", e)
			}
			if profile != "active-exchange-close" {
				select {
				case <-entered:
					t.Fatal("invalid callback contacted token endpoint")
				default:
				}
			}
			select {
			case <-session.done:
			case <-time.After(2 * time.Second):
				t.Fatal("callback listener remains")
			}
		})
	}
}

func (p openAITestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	address, _ := url.Parse(p.address)
	copy.URL.Scheme = address.Scheme
	copy.URL.Host = address.Host
	copy.Host = address.Host
	return p.base.RoundTrip(copy)
}

func TestDesktopOpenAIOwnedCallbackActualTransportStorageRefreshAndClose(t *testing.T) {
	var mu sync.Mutex
	calls := []url.Values{}
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/oauth/token" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("User-Agent") != OpenAICodexTokenUserAgent || r.Header.Get("Authorization") != "" {
			t.Error("token wire changed")
		}
		data, _ := io.ReadAll(r.Body)
		v, _ := url.ParseQuery(string(data))
		mu.Lock()
		calls = append(calls, v)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if v.Get("grant_type") == "authorization_code" {
			_, _ = w.Write([]byte(`{"access_token":"fake-access","refresh_token":"fake-refresh","expires_in":3600}`))
		} else {
			_, _ = w.Write([]byte(`{"access_token":"fake-rotated","expires_in":7200}`))
		}
	}))
	defer tokens.Close()
	home := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	parent, e := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return now }, HTTPClient: &http.Client{Transport: openAITestTransport{tokens.URL, http.DefaultTransport}}, Settings: func(context.Context) (protocol.JSONObject, error) {
		return object([]byte(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer parent.Close()
	port := 0
	service := NewOpenAIService(parent, &port)
	defer service.Close()
	first, e := service.StartSession(54321)
	if e != nil {
		t.Fatal(e)
	}
	second, e := service.StartSession(54322)
	if e != nil {
		t.Fatal(e)
	}
	if service.GetSession(first.State) != nil {
		t.Fatal("superseded pending session remained")
	}
	select {
	case <-first.done:
	default:
		t.Fatal("superseded callback listener was not joined")
	}
	// An OS may immediately allocate the same ephemeral port to the replacement.
	// The old listener is joined above; only dial when it is a different address.
	if first.RedirectURI != second.RedirectURI {
		if conn, e := net.DialTimeout("tcp", strings.TrimPrefix(strings.Split(first.RedirectURI, "/auth/")[0], "http://"), time.Second); e == nil {
			conn.Close()
			t.Fatal("old callback listener remained")
		}
	}
	if len(second.State) != 64 || len(second.CodeVerifier) != 128 || strings.Contains(second.RedirectURI, "54322") {
		t.Fatal("callback/PKCE contract changed")
	}
	r, e := http.Get(second.RedirectURI + "?code=fixture%2Bcode&state=" + second.State)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || string(body) != OpenAICallbackPage(true, "", false) {
		t.Fatalf("callback %d %s", r.StatusCode, body)
	}
	doc, e := service.LoadTokens(context.Background())
	if e != nil || doc.Record().String("accessToken") != "fake-access" {
		t.Fatal(e)
	}
	info, _ := os.Stat(service.TokenPath())
	if info.Mode().Perm() != 0600 {
		t.Fatal("token mode")
	}
	mu.Lock()
	wire := calls[0]
	mu.Unlock()
	if wire.Get("code") != "fixture+code" || wire.Get("code_verifier") != second.CodeVerifier || wire.Get("redirect_uri") != second.RedirectURI || wire.Get("client_id") != OpenAICodexClientID {
		t.Fatal("actual consumed PKCE not exchanged")
	}
	if _, e = service.CompleteSession(context.Background(), "replay", second.State); e == nil {
		t.Fatal("state replay succeeded")
	}
	now = now.Add(56 * time.Minute)
	doc, e = service.EnsureFreshTokens(context.Background())
	if e != nil || doc.Record().String("accessToken") != "fake-rotated" || doc.Record().String("refreshToken") != "fake-refresh" {
		t.Fatal("refresh", e)
	}
	mu.Lock()
	if len(calls) != 2 || calls[1].Get("scope") != "openid profile email" {
		t.Error("refresh wire")
	}
	mu.Unlock()
	// The legacy file remains readable without a new schema marker or rewriting
	// unknown fields when no refresh is needed.
	legacy := `{"accessToken":"legacy","refreshToken":null,"expiresAt":null,"unknown":{"keep":true}}`
	if e = os.WriteFile(service.TokenPath(), []byte(legacy), 0600); e != nil {
		t.Fatal(e)
	}
	doc, e = service.EnsureFreshTokens(context.Background())
	if e != nil || doc.Record().String("accessToken") != "legacy" {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(service.TokenPath())
	if string(data) != legacy {
		t.Fatal("legacy file rewritten")
	}
	third, e := service.StartSession(10)
	if e != nil {
		t.Fatal(e)
	}
	service.Close()
	if conn, e := net.DialTimeout("tcp", strings.TrimPrefix(strings.Split(third.RedirectURI, "/auth/")[0], "http://"), time.Second); e == nil {
		conn.Close()
		t.Fatal("close leaked callback")
	}
	if e = service.DeleteTokens(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(service.TokenPath()); !os.IsNotExist(e) {
		t.Fatal("logout retained token")
	}
}
