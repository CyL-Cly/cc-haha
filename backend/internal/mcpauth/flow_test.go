package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type oauthRequest struct{ Method, Path, Body, Authorization string }

func freeCallbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
func TestActualOAuthDCRCallbackPKCEExchangeStorageRefreshAndOwnedClear(t *testing.T) {
	s, _ := authFixture(t)
	var host *httptest.Server
	var mutex sync.Mutex
	requests := []oauthRequest{}
	challenge := ""
	redirect := ""
	host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mutex.Lock()
		requests = append(requests, oauthRequest{r.Method, r.URL.RequestURI(), string(body), r.Header.Get("Authorization")})
		mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource":
			w.WriteHeader(404)
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": host.URL, "authorization_endpoint": host.URL + "/authorize", "token_endpoint": host.URL + "/token", "registration_endpoint": host.URL + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read", "offline_access"}})
		case "/register":
			var registration map[string]any
			if json.Unmarshal(body, &registration) != nil {
				t.Error("registration was not JSON")
			}
			registration["client_id"] = "registered-client"
			_ = json.NewEncoder(w).Encode(registration)
		case "/token":
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Error(err)
			}
			if form.Get("grant_type") == "authorization_code" {
				sum := sha256.Sum256([]byte(form.Get("code_verifier")))
				mutex.Lock()
				wantedChallenge, wantedRedirect := challenge, redirect
				mutex.Unlock()
				if base64.RawURLEncoding.EncodeToString(sum[:]) != wantedChallenge || form.Get("redirect_uri") != wantedRedirect || form.Get("code") != "issued-code" || form.Get("client_id") != "registered-client" {
					t.Error("PKCE/callback/client binding failed")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "first-access", "token_type": "Bearer", "refresh_token": "rotating-refresh", "expires_in": 600, "scope": "read offline_access"})
			} else if form.Get("grant_type") == "refresh_token" {
				if form.Get("refresh_token") != "rotating-refresh" {
					t.Error("refresh did not use actual stored credential")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "refreshed-access", "token_type": "Bearer", "expires_in": 3600, "scope": "read offline_access"})
			} else {
				w.WriteHeader(400)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer host.Close()
	port := freeCallbackPort(t)
	config := record(field("type", "http"), field("url", host.URL+"/mcp"), field("oauth", map[string]any{"callbackPort": port}))
	var callbackBody string
	delivered := make(chan struct{})
	err := s.Authenticate(context.Background(), "actual-source-shape", config, func(address string) error {
		defer close(delivered)
		parsed, err := url.Parse(address)
		if err != nil {
			return err
		}
		if parsed.Query().Get("scope") != "read offline_access" || parsed.Query().Get("prompt") != "consent" {
			return errors.New("metadata scope selection lost")
		}
		mutex.Lock()
		challenge = parsed.Query().Get("code_challenge")
		redirect = parsed.Query().Get("redirect_uri")
		wantedRedirect := redirect
		mutex.Unlock()
		callback := wantedRedirect + "?code=issued-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
		response, err := host.Client().Get(callback)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		mutex.Lock()
		callbackBody = string(body)
		mutex.Unlock()
		return err
	})
	<-delivered
	mutex.Lock()
	observedCallback := callbackBody
	mutex.Unlock()
	if err != nil || !strings.Contains(observedCallback, "Authentication Successful") {
		t.Fatalf("actual OAuth flow err=%v callback=%q", err, observedCallback)
	}
	provider, err := s.Provider(context.Background(), "actual-source-shape", config)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := provider.Token(context.Background()); err != nil || token != "first-access" {
		t.Fatalf("actual saved token=%q err=%v", token, err)
	}
	entry, err := s.entry(context.Background(), provider.key)
	if err != nil {
		t.Fatal(err)
	}
	entry.Set("expiresAt", jsonNumber(float64(s.options.Now().UnixMilli()+1000)))
	if err = provider.updateEntry(context.Background(), func(value protocol.JSONObject) error { value.Set("expiresAt", entry.Get("expiresAt")); return nil }); err != nil {
		t.Fatal(err)
	}
	if token, err := provider.Token(context.Background()); err != nil || token != "refreshed-access" {
		t.Fatalf("real refresh=%q err=%v", token, err)
	}
	entry, err = s.entry(context.Background(), provider.key)
	if err != nil {
		t.Fatal(err)
	}
	if entry.String("refreshToken") != "rotating-refresh" {
		t.Fatal("SDK refresh fallback did not retain rotating token")
	}
	if err = s.Clear(context.Background(), "actual-source-shape", config); err != nil {
		t.Fatal(err)
	}
	entry, err = s.entry(context.Background(), provider.key)
	if err != nil || len(entry.OwnProperties()) != 0 {
		t.Fatal("owned clear did not remove OAuth slot")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal("callback port was leaked", err)
	}
	_ = listener.Close()
}
func TestUnauthorizedProbeDoesNotOpenBrowserAndOnlyReturnsNeedsAuth(t *testing.T) {
	s, _ := authFixture(t)
	opened := false
	s.options.OpenBrowser = func(context.Context, string) (bool, error) { opened = true; return true, nil }
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer host.Close()
	config := record(field("type", "http"), field("url", host.URL+"/mcp"))
	provider, err := s.Provider(context.Background(), "no-browser-probe", config)
	if err != nil {
		t.Fatal(err)
	}
	err = provider.Authorize(context.Background(), mcpclient.AuthChallenge{URL: host.URL, Status: 401, WWWAuthenticate: "Bearer realm=fixture"})
	var auth *mcpclient.AuthError
	if !errors.As(err, &auth) || opened {
		t.Fatalf("probe invented authorization or opened browser: %v opened=%v", err, opened)
	}
}
func TestOAuthCallbackMismatchCancellationAndOccupiedPortReleaseResources(t *testing.T) {
	for _, kind := range []string{"mismatch", "cancel", "denied"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := authFixture(t)
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
			defer host.Close()
			port := freeCallbackPort(t)
			config := record(field("type", "http"), field("url", host.URL+"/mcp"), field("oauth", map[string]any{"clientId": "preconfigured", "callbackPort": port}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := s.Authenticate(ctx, "callback-test", config, func(address string) error {
				parsed, _ := url.Parse(address)
				callback := parsed.Query().Get("redirect_uri")
				if kind == "cancel" {
					cancel()
					return nil
				}
				suffix := "?code=x&state=wrong"
				if kind == "denied" {
					suffix = "?error=access_denied&error_description=declined"
				}
				response, err := host.Client().Get(callback + suffix)
				if err != nil {
					return err
				}
				_ = response.Body.Close()
				return nil
			})
			if err == nil {
				t.Fatal("failed/cancelled callback reported success")
			}
			listener, bindErr := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
			if bindErr != nil {
				t.Fatal("failed callback left listener open", bindErr)
			}
			_ = listener.Close()
		})
	}
	s, _ := authFixture(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	config := record(field("type", "http"), field("url", "http://127.0.0.1:1/mcp"), field("oauth", map[string]any{"clientId": "preconfigured", "callbackPort": port}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = s.Authenticate(ctx, "occupied", config, nil)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("occupied callback port not actionable: %v", err)
	}
}
