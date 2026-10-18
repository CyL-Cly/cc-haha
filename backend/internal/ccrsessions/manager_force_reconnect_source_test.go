package ccrsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

type forceReconnectPeer struct {
	upgrades       atomic.Int32
	firstEntered   chan struct{}
	firstCancelled chan struct{}
	secondClosed   chan struct{}
	secondEntered  chan struct{}
}

func newForceReconnectPeer() *forceReconnectPeer {
	return &forceReconnectPeer{firstEntered: make(chan struct{}), firstCancelled: make(chan struct{}), secondClosed: make(chan struct{}), secondEntered: make(chan struct{})}
}

func (p *forceReconnectPeer) serve(t *testing.T, w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/fixture/first-upgrade-entered" {
		select {
		case <-p.firstEntered:
			ccrJSON(w, map[string]bool{"entered": true})
		case <-request.Context().Done():
		}
		return
	}
	if request.URL.Path == "/fixture/observed" {
		select {
		case <-p.firstCancelled:
		case <-request.Context().Done():
			return
		}
		select {
		case <-p.secondClosed:
			ccrJSON(w, map[string]any{"upgrades": p.upgrades.Load(), "firstCancelled": true, "secondClosed": true})
		case <-request.Context().Done():
		}
		return
	}
	if request.URL.Path != "/v1/sessions/ws/session_owned_force/subscribe" || request.URL.Query().Get("organization_uuid") != "owned-org" || request.Header.Get("Authorization") != "Bearer owned-force-token" || request.Header.Get("anthropic-version") != "2023-06-01" {
		t.Error("unowned force-reconnect request", request.Method, request.URL, request.Header)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	_, _ = io.Copy(io.Discard, request.Body)
	if p.upgrades.Add(1) == 1 {
		close(p.firstEntered)
		// Hold the actual upgrade, not a mocked websocket or callback.
		<-request.Context().Done()
		close(p.firstCancelled)
		return
	}
	if p.upgrades.Load() != 2 {
		t.Error("unexpected duplicate connection", p.upgrades.Load())
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	connection, err := websocket.Accept(w, request, nil)
	if err != nil {
		t.Error(err)
		return
	}
	defer connection.CloseNow()
	defer close(p.secondClosed)
	close(p.secondEntered)
	for {
		if _, _, err := connection.Read(request.Context()); err != nil {
			return
		}
	}
}

func TestActualSourceAndRemoteManagerForceReconnectDuringHeldHandshake(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	t.Run("whole-Source", func(t *testing.T) {
		p := newForceReconnectPeer()
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
		defer peer.Close()
		home := t.TempDir()
		config := filepath.Join(home, ".claude")
		if err := os.MkdirAll(config, 0700); err != nil {
			t.Fatal(err)
		}
		ca := filepath.Join(home, "private-ca.pem")
		if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
		fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-force-reconnect.fixture.ts")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=ant", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "USE_LOCAL_OAUTH=1", "CLAUDE_LOCAL_OAUTH_API_BASE=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_FORCE_FIXTURE=1"}
		payload, err := json.Marshal(map[string]string{"home": home, "peer": peer.URL})
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdin = bytes.NewReader(payload)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatal("actual Source reconnect", err, string(output), stderr.String())
		}
		var result struct {
			Connections []json.RawMessage
			Transport   struct {
				Upgrades       int32
				FirstCancelled bool
				SecondClosed   bool
			}
			Caps struct{ Blocked []string }
		}
		if err = json.Unmarshal(output, &result); err != nil || len(result.Caps.Blocked) != 0 || len(result.Connections) != 2 || result.Transport.Upgrades != 2 || !result.Transport.FirstCancelled || !result.Transport.SecondClosed {
			t.Fatal("original reconnect/real peer mismatch", err, string(output), stderr.String())
		}
		t.Log("actual original Source: first held handshake cancelled, second connected/closed, original500ms timer, caps0", string(output))
	})
	t.Run("actual-Go", func(t *testing.T) {
		p := newForceReconnectPeer()
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
		defer peer.Close()
		root := t.TempDir()
		s, err := New(Options{BaseURL: peer.URL, CWD: root, ConfigDir: root, TempDir: root, HTTP: peer.Client(), Credentials: func(context.Context, bool) (Credentials, error) {
			return Credentials{Token: "owned-force-token", Organization: "owned-org", Scopes: []string{"user:inference"}}, nil
		}, Settings: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, GenerateTitle: func(context.Context, string) (Title, error) { return Title{}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		connected := make(chan struct{}, 1)
		manager, err := s.NewRemoteManager(ctx, "session_owned_force", ManagerCallbacks{State: func(state string) {
			if state == "connected" {
				connected <- struct{}{}
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		manager.Start()
		select {
		case <-p.firstEntered:
		case <-ctx.Done():
			t.Fatal("actual first upgrade did not enter")
		}
		manager.Reconnect()
		select {
		case <-connected:
		case <-ctx.Done():
			t.Fatal("force reconnect retained the old held Dial and did not connect a replacement", p.upgrades.Load())
		}
		select {
		case <-p.firstCancelled:
		case <-ctx.Done():
			t.Fatal("replacement connected while old handshake remained live")
		}
		manager.Close()
		select {
		case <-p.secondClosed:
		case <-ctx.Done():
			t.Fatal("actual replacement WS did not close")
		}
		if p.upgrades.Load() != 2 {
			t.Fatal("actual force reconnect cardinality", p.upgrades.Load())
		}
	})
}
