package ccrsessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

type reconnectRoundTripper func(*http.Request) (*http.Response, error)

func (f reconnectRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func actualForceReconnectService(t *testing.T, peer *httptest.Server, client *http.Client) *Service {
	t.Helper()
	root := t.TempDir()
	s, err := New(Options{BaseURL: peer.URL, CWD: root, ConfigDir: root, TempDir: root, HTTP: client, Credentials: func(context.Context, bool) (Credentials, error) {
		return Credentials{Token: "owned-force-token", Organization: "owned-org", Scopes: []string{"user:inference"}}, nil
	}, Settings: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, GenerateTitle: func(context.Context, string) (Title, error) { return Title{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestRemoteManagerForceReconnectRejectsLateActual101(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var upgrades atomic.Int32
	firstClosed, secondClosed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/sessions/ws/session_owned_force/subscribe" || request.URL.Query().Get("organization_uuid") != "owned-org" || request.Header.Get("Authorization") != "Bearer owned-force-token" || request.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("unowned late-101 request", request.Method, request.URL, request.Header)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		index := upgrades.Add(1)
		if index > 2 {
			t.Error("duplicate reconnect upgrade", index)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		if index == 1 {
			defer close(firstClosed)
		} else {
			defer close(secondClosed)
		}
		for {
			if _, _, err := connection.Read(request.Context()); err != nil {
				return
			}
		}
	}))
	defer peer.Close()
	client := peer.Client()
	transport := client.Transport
	headersReceived, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseHeaders := func() { releaseOnce.Do(func() { close(release) }) }
	// Hold only delivery of the first real successful HTTP upgrade. The server,
	// TLS, response headers, and response body all come from the native transport.
	var responses atomic.Int32
	client.Transport = reconnectRoundTripper(func(request *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(request)
		if err == nil && responses.Add(1) == 1 {
			if response.StatusCode != http.StatusSwitchingProtocols {
				t.Error("actual first upgrade was not 101", response.StatusCode)
			}
			close(headersReceived)
			<-release
		}
		return response, err
	})
	s := actualForceReconnectService(t, peer, client)
	var publications atomic.Int32
	connected := make(chan struct{}, 2)
	manager, err := s.NewRemoteManager(ctx, "session_owned_force", ManagerCallbacks{State: func(state string) {
		if state == "connected" {
			publications.Add(1)
			connected <- struct{}{}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	defer releaseHeaders()
	manager.Start()
	select {
	case <-headersReceived:
	case <-ctx.Done():
		t.Fatal("native first 101 did not enter delivery hold")
	}
	manager.Reconnect()
	releaseHeaders()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("late old upgrade prevented replacement", upgrades.Load(), publications.Load())
	}
	select {
	case <-firstClosed:
	case <-ctx.Done():
		t.Fatal("retired actual socket remained live")
	}
	manager.Close()
	select {
	case <-secondClosed:
	case <-ctx.Done():
		t.Fatal("replacement actual socket was not joined")
	}
	if upgrades.Load() != 2 || publications.Load() != 1 {
		t.Fatal("stale upgrade published or duplicate connection", upgrades.Load(), publications.Load())
	}
}

func TestRemoteManagerCloseCancelsOriginalForceReconnectWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := newForceReconnectPeer()
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { p.serve(t, w, request) }))
	defer peer.Close()
	s := actualForceReconnectService(t, peer, peer.Client())
	originalWait := s.options.Wait
	waitEntered := make(chan time.Duration, 1)
	s.options.Wait = func(ctx context.Context, duration time.Duration) error {
		waitEntered <- duration
		// Observe admission, then use the original native timer and cancellation.
		return originalWait(ctx, duration)
	}
	manager, err := s.NewRemoteManager(ctx, "session_owned_force", ManagerCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Start()
	select {
	case <-p.firstEntered:
	case <-ctx.Done():
		t.Fatal("native held upgrade did not enter")
	}
	manager.Reconnect()
	select {
	case duration := <-waitEntered:
		if duration != 500*time.Millisecond {
			t.Fatal("force reconnect changed the original wait", duration)
		}
	case <-ctx.Done():
		t.Fatal("retired native attempt did not reach original reconnect wait")
	}
	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel and join reconnect wait")
	}
	select {
	case <-p.firstCancelled:
	case <-ctx.Done():
		t.Fatal("original held upgrade did not observe cancellation")
	}
	if p.upgrades.Load() != 1 {
		t.Fatal("closed manager started a late replacement", p.upgrades.Load())
	}
}
