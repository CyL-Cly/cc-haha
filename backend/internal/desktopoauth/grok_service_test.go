package desktopoauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopGrokCallbackRejectExpiryAndCloseCancelsActualExchange(t *testing.T) {
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
			service := NewGrokService(parent, &port)
			defer service.Close()
			session, e := service.StartSession()
			if e != nil {
				t.Fatal(e)
			}
			if profile == "expiry" {
				now.Add(600000)
				if service.GetSession(session.State) == nil {
					t.Fatal("Source exact TTL boundary changed")
				}
				now.Add(1)
				if service.GetSession(session.State) != nil {
					t.Fatal("expired state remained")
				}
				service.Dispose()
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

func TestGrokConsumedSessionTimerKeepsActualCallbackUntilOwnerClose(t *testing.T) {
	home := t.TempDir()
	parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	service := parent.Grok()
	session, err := service.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	if service.ConsumeSession(session.State) != session {
		t.Fatal("actual state not consumed")
	}
	service.expireRegisteredSession(session)
	select {
	case <-session.done:
		t.Fatal("Source timer closed consumed callback")
	default:
	}
	service.Dispose()
	select {
	case <-session.done:
		t.Fatal("Source dispose closed consumed callback")
	default:
	}
	if err = parent.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("native Close did not join owned consumed callback")
	}
}
