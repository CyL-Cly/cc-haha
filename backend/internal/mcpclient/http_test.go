package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type controlledAuth struct {
	mu    sync.Mutex
	token string
	calls []AuthChallenge
	err   error
}

func (a *controlledAuth) Token(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token, nil
}
func (a *controlledAuth) Authorize(_ context.Context, c AuthChallenge) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, c)
	a.token = "fake-authorized"
	return a.err
}

func TestAuthenticationChallengeRefreshAndCircuitBreaker(t *testing.T) {
	for _, status := range []int{401, 403} {
		for _, keepFailing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, keepFailing), func(t *testing.T) {
				auth := &controlledAuth{token: "fake-old"}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						w.WriteHeader(405)
						return
					}
					if r.Header.Get("Authorization") != "Bearer fake-authorized" || keepFailing {
						w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.invalid/metadata", error="insufficient_scope", scope="owned:read"`)
						w.WriteHeader(status)
						return
					}
					b, _ := io.ReadAll(r.Body)
					message, _ := decodeMessage(b)
					if len(message.ID) == 0 {
						w.WriteHeader(202)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(raw(fixtureResult(message)))
				}))
				defer server.Close()
				o := options(t.TempDir())
				o.Auth = auth
				c, err := Connect(context.Background(), Config{Type: "http", URL: server.URL}, o)
				if keepFailing {
					var h *HTTPError
					if !errors.As(err, &h) || h.Status != status {
						t.Fatalf("circuit: %#v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					_ = c.Close()
				}
				if len(auth.calls) != 1 || auth.calls[0].Status != status || !strings.Contains(auth.calls[0].WWWAuthenticate, "owned:read") {
					t.Fatalf("challenges: %#v", auth.calls)
				}
			})
		}
	}
}

func TestUnauthenticatedAndFailedAuthorizationNeverBecomeReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer scope="read"`)
		w.WriteHeader(401)
	}))
	defer server.Close()
	o := options(t.TempDir())
	_, err := Connect(context.Background(), Config{Type: "http", URL: server.URL}, o)
	var a *AuthError
	if !errors.As(err, &a) || a.Challenge.Status != 401 {
		t.Fatalf("needs auth: %#v", err)
	}
	o.Auth = &controlledAuth{err: errors.New("offline login declined")}
	_, err = Connect(context.Background(), Config{Type: "http", URL: server.URL}, o)
	if err == nil || err.Error() != "offline login declined" {
		t.Fatalf("failed auth: %#v", err)
	}
}

func TestHTTPTimeoutFreshPerRequestAndSSEBodyLifetime(t *testing.T) {
	var mu sync.Mutex
	delayHeaders := false
	delayBody := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(405)
			return
		}
		b, _ := io.ReadAll(r.Body)
		message, _ := decodeMessage(b)
		if len(message.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		mu.Lock()
		headers, body := delayHeaders, delayBody
		mu.Unlock()
		if headers {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		if body {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(60 * time.Millisecond):
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(fixtureResult(message)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(fixtureResult(message)))
	}))
	defer server.Close()
	o := options(t.TempDir())
	o.HTTPTimeout = 20 * time.Millisecond
	c := mustConnect(t, Config{Type: "http", URL: server.URL}, o)
	time.Sleep(30 * time.Millisecond)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("stale request timeout: %v", err)
	}
	mu.Lock()
	delayBody = true
	mu.Unlock()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("timeout incorrectly killed SSE body: %v", err)
	}
	mu.Lock()
	delayBody = false
	delayHeaders = true
	mu.Unlock()
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("header timeout did not fire")
	}
	mu.Lock()
	delayHeaders = false
	mu.Unlock()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("timeout poisoned later requests: %v", err)
	}
}

func newResumptionServer(t *testing.T, fail bool) (*httptest.Server, *[]string, *sync.Mutex) {
	mu := &sync.Mutex{}
	gets := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			id := r.Header.Get("Last-Event-ID")
			mu.Lock()
			*gets = append(*gets, id)
			mu.Unlock()
			if id == "" {
				w.WriteHeader(405)
				return
			}
			if fail {
				w.WriteHeader(503)
				return
			}
			if r.Header.Get("Mcp-Session-Id") != "resume-session" || r.Header.Get("Mcp-Protocol-Version") != LatestProtocolVersion {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"resumed"}]}}`+"\n\n")
			return
		}
		b, _ := io.ReadAll(r.Body)
		message, _ := decodeMessage(b)
		if len(message.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		if message.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "resume-session")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw(fixtureResult(message)))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: resume-1\nretry: 1\ndata:\n\n")
	}))
	t.Cleanup(server.Close)
	return server, gets, mu
}
func TestStreamResumptionDoesNotRepeatToolSideEffect(t *testing.T) {
	server, gets, mu := newResumptionServer(t, false)
	c := mustConnect(t, Config{Type: "http", URL: server.URL}, options(t.TempDir()))
	result, err := c.CallTool(context.Background(), "owned-side-effect", object(), protocol.JSONObject{}, RequestOptions{})
	if err != nil || !strings.Contains(string(result.Get("content")), "resumed") {
		t.Fatalf("resume: %v %s", err, raw(result))
	}
	mu.Lock()
	defer mu.Unlock()
	found := 0
	for _, id := range *gets {
		if id == "resume-1" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("resumption requests %#v", *gets)
	}
}
func TestReconnectExhaustionClosesAndRejectsPending(t *testing.T) {
	server, gets, mu := newResumptionServer(t, true)
	c := mustConnect(t, Config{Type: "http", URL: server.URL}, options(t.TempDir()))
	_, err := c.CallTool(context.Background(), "owned-side-effect", object(), protocol.JSONObject{}, RequestOptions{Timeout: time.Second})
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32000 {
		t.Fatalf("reconnect exhaustion: %#v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	found := 0
	for _, id := range *gets {
		if id == "resume-1" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("retry bound %#v", *gets)
	}
}

func TestLegacyEndpointRejectsCrossOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: endpoint\ndata: https://example.invalid/messages\n\n")
	}))
	defer server.Close()
	_, err := Connect(context.Background(), Config{Type: "sse", URL: server.URL}, options(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "Endpoint origin does not match") {
		t.Fatalf("cross origin endpoint: %#v", err)
	}
}

func TestActiveConnectionKeepsResolvedHeaderSnapshot(t *testing.T) {
	f := newFixtureServer(t, false)
	config := f.config()
	c := mustConnect(t, config, options(t.TempDir()))
	config.Headers["X-Owned"] = "edited-after-connect"
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.snapshot()[2]["custom"] != "test" {
		t.Fatal("active client mixed old session with replacement configuration")
	}
}

func TestServerRequestCancellationIsRegisteredBeforeNextMessage(t *testing.T) {
	held := make(chan struct{}, 1)
	replies := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(405)
			return
		}
		body, _ := io.ReadAll(r.Body)
		message, _ := decodeMessage(body)
		if message.Method == "" {
			replies <- string(message.ID)
			w.WriteHeader(202)
			return
		}
		if len(message.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		if message.Method == "hold-server" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"server-held\",\"method\":\"owned/hold\",\"params\":{}}\n\n"+"data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\",\"params\":{\"requestId\":\"server-held\"}}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-held:
			case <-r.Context().Done():
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(fixtureResult(message)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(fixtureResult(message)))
	}))
	defer server.Close()
	o := options(t.TempDir())
	o.OnRequest = func(ctx context.Context, method string, params protocol.JSONObject) (protocol.JSONObject, error) {
		if method != "owned/hold" {
			return object(), nil
		}
		<-ctx.Done()
		held <- struct{}{}
		return object(), nil
	}
	c := mustConnect(t, Config{Type: "http", URL: server.URL}, o)
	if _, err := c.Request(context.Background(), "hold-server", object(), RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-replies:
		t.Fatalf("cancelled server request replied: %s", id)
	default:
	}
}

func TestResumptionExhaustionExpiryAndCancelMatchOriginal(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	for _, scenario := range []string{"resume", "exhaust", "expired", "cancel", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			var goConfig, sourceConfig Config
			var goFixture, sourceFixture *fixtureServer
			requests := []contractRequest{{Method: "tools/call", Params: object(field("name", "owned-side-effect"), field("arguments", object()))}}
			if scenario == "resume" || scenario == "exhaust" {
				goServer, _, _ := newResumptionServer(t, scenario == "exhaust")
				sourceServer, _, _ := newResumptionServer(t, scenario == "exhaust")
				goConfig = Config{Type: "http", URL: goServer.URL}
				sourceConfig = Config{Type: "http", URL: sourceServer.URL}
			} else {
				goFixture = newFixtureServer(t, false)
				sourceFixture = newFixtureServer(t, false)
				goConfig = goFixture.config()
				sourceConfig = sourceFixture.config()
				if scenario == "expired" {
					goFixture.status["expired"] = 404
					sourceFixture.status["expired"] = 404
					requests = []contractRequest{{Method: "expired"}}
				} else {
					requests = []contractRequest{{Method: "hang", CancelAfter: 15}}
					if scenario == "timeout" {
						requests[0].CancelAfter = 0
						requests[0].Timeout = 20
					}
					requests = append(requests, contractRequest{Method: "await-cancel"})
				}
			}
			c := mustConnect(t, goConfig, options(home))
			actual, _ := runContract(t, c, requests)
			_ = c.Close()
			expected := originalContract(t, bun, home, sourceConfig, requests)
			assertJSONEqual(t, raw(actual), raw(expected["values"]))
			if scenario == "cancel" || scenario == "timeout" {
				cancelParams := func(f *fixtureServer) any {
					for _, row := range f.snapshot() {
						if message, ok := row["message"].(map[string]any); ok && message["method"] == "notifications/cancelled" {
							return message["params"]
						}
					}
					return nil
				}
				assertJSONEqual(t, raw(cancelParams(goFixture)), raw(cancelParams(sourceFixture)))
				if cancelParams(goFixture) == nil || cancelParams(sourceFixture) == nil {
					t.Fatal("No real cancellation reached transport")
				}
			}
		})
	}
}
