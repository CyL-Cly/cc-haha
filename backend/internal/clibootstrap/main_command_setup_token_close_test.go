package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
)

func TestActualSetupTokenOwnerCloseJoinsWaitingBrowserListenerAndHeldTokenHTTP(t *testing.T) {
	for _, held := range []string{"browser", "exchange", "profile"} {
		t.Run(held, func(t *testing.T) {
			m := authCommandFixture(t, map[string]any{"env": map[string]string{}})
			m.Output = &bytes.Buffer{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			admitted := make(chan struct{})
			httpDone := make(chan struct{})
			browserDone := make(chan struct{})
			var once sync.Once
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				target := held == "exchange" && r.URL.Path == "/v1/oauth/token" || held == "profile" && r.URL.Path == "/api/oauth/profile"
				if target {
					once.Do(func() { close(admitted) })
					<-r.Context().Done()
					close(httpDone)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/oauth/token" {
					fmt.Fprint(w, `{"access_token":"FAKE_OWNER_SECRET","scope":"user:inference","expires_in":31536000}`)
					return
				}
				w.WriteHeader(403)
			}))
			defer peer.Close()
			config, _ := desktopoauth.ResolveConfig(m.Native.Environment)
			config.TokenURL = peer.URL + "/v1/oauth/token"
			config.BaseAPIURL = peer.URL
			address := make(chan string, 1)
			done := make(chan error, 1)
			go func() {
				done <- m.Run(ctx, func(scope *cooperative.Scope) error {
					_, e := m.SetupToken(ctx, scope, SetupTokenOptions{HTTP: peer.Client(), Config: &config, Input: input, Browser: func(ctx context.Context, uri string) error {
						defer close(browserDone)
						parsed, _ := url.Parse(uri)
						callback, _ := url.Parse(parsed.Query().Get("redirect_uri"))
						address <- callback.Host
						if held == "browser" {
							close(admitted)
							<-ctx.Done()
							return context.Cause(ctx)
						}
						q := callback.Query()
						q.Set("code", "physical-authorization-code")
						q.Set("state", parsed.Query().Get("state"))
						callback.RawQuery = q.Encode()
						request, _ := http.NewRequestWithContext(ctx, "GET", callback.String(), nil)
						client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
						response, e := client.Do(request)
						if response != nil {
							response.Body.Close()
						}
						return e
					}})
					return e
				})
			}()
			var callback string
			select {
			case callback = <-address:
			case <-ctx.Done():
				t.Fatal("physical browser/listener not ready")
			}
			select {
			case <-admitted:
			case <-ctx.Done():
				t.Fatal("actual selected boundary not entered")
			}
			closeDone := make(chan struct{})
			go func() { m.Close(); close(closeDone) }()
			select {
			case <-closeDone:
			case <-ctx.Done():
				t.Fatal("owner Close didn't join active setup-token")
			}
			select {
			case <-browserDone:
			default:
				t.Fatal("owner Close left browser primitive active")
			}
			if held != "browser" {
				select {
				case <-httpDone:
				case <-ctx.Done():
					t.Fatal("owner Close left actual HTTP worker active")
				}
			}
			connection, e := net.DialTimeout("tcp", callback, time.Second)
			if e == nil {
				connection.Close()
				t.Fatal("physical callback listener survived owner Close")
			}
			select {
			case e := <-done:
				if e == nil || !errors.Is(e, cooperative.ErrClosed) {
					t.Fatalf("close cause %v", e)
				}
			case <-ctx.Done():
				t.Fatal("MainCommand.Run did not settle")
			}
			m.Close()
		})
	}
}
