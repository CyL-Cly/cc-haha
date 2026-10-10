package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parse(t *testing.T, text string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if err := o.UnmarshalJSON([]byte(text)); err != nil {
		t.Fatal(err)
	}
	return o
}
func manager(t *testing.T, enabled func(context.Context, string, protocol.JSONObject) (bool, error)) *Service {
	t.Helper()
	if enabled == nil {
		enabled = func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }
	}
	s, err := New(Options{CWD: t.TempDir(), Environment: map[string]string{}, Version: "fixture", Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) { return nil, nil }, Enabled: enabled, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error {
		return errors.New("explicit authorization primitive not requested in this test")
	}, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func loopback(t *testing.T, mode *atomic.Int32, initializes, lists *atomic.Int32, entered chan struct{}, release chan struct{}) *httptest.Server {
	t.Helper()
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(405)
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(200)
			return
		}
		if mode.Load() == 1 {
			w.Header().Set("WWW-Authenticate", `Bearer scope="read"`)
			w.WriteHeader(401)
			return
		}
		var message protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			w.WriteHeader(400)
			return
		}
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result protocol.JSONObject
		switch message.String("method") {
		case "initialize":
			initializes.Add(1)
			if entered != nil {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			capabilities := `{"tools":{}}`
			if mode.Load() == 3 {
				capabilities = `{}`
			}
			result = parse(t, `{"protocolVersion":"2025-11-25","serverInfo":{"name":"owned-loopback","version":"1"},"capabilities":`+capabilities+`}`)
		case "tools/list":
			lists.Add(1)
			if mode.Load() == 2 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: []byte(`"2.0"`)}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "error", Value: []byte(`{"code":-32603,"message":"owned discovery failure"}`)}))
				return
			}
			result = parse(t, `{"tools":[{"name":"read","inputSchema":{"type":"object"}}]}`)
		default:
			result = protocol.NewJSONObject()
		}
		raw, _ := result.MarshalJSON()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: []byte(`"2.0"`)}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: raw}))
	}))
	t.Cleanup(peer.Close)
	return peer
}
func TestActualPeerCachePolicyRefreshAndDiscoveryFailure(t *testing.T) {
	var mode, initializes, lists atomic.Int32
	var enabled atomic.Bool
	enabled.Store(true)
	peer := loopback(t, &mode, &initializes, &lists, nil, nil)
	s := manager(t, func(context.Context, string, protocol.JSONObject) (bool, error) { return enabled.Load(), nil })
	config := parse(t, `{"type":"http","url":"`+peer.URL+`","scope":"dynamic","pluginSource":"owned@market"}`)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, count, err := s.Probe(context.Background(), "owned", config)
			if err != nil || status != "connected" || count != 1 {
				t.Error(status, count, err)
			}
		}()
	}
	wg.Wait()
	if initializes.Load() != 1 || lists.Load() != 1 {
		t.Fatal("concurrent probe duplicated initialization/discovery", initializes.Load(), lists.Load())
	}
	value, err := s.Connect(context.Background(), "owned", config)
	if err != nil {
		t.Fatal(err)
	}
	value.Tools[0].Set("name", []byte(`"mutated"`))
	value.Config.Set("url", []byte(`"wrong"`))
	value, err = s.Connect(context.Background(), "owned", config)
	if err != nil || value.Tools[0].String("name") != "read" || value.Config.String("url") != peer.URL {
		t.Fatal("cache leaked mutable discovery snapshots", value, err)
	}
	enabled.Store(false)
	status, count, err := s.Probe(context.Background(), "owned", config)
	if err != nil || status != "disabled" || count != 0 {
		t.Fatal(status, count, err)
	}
	if err := s.Disconnect(context.Background(), "owned", config); err != nil {
		t.Fatal(err)
	}
	enabled.Store(true)
	mode.Store(1)
	status, count, err = s.Probe(context.Background(), "owned", config)
	if err != nil || status != "needs-auth" || count != 0 {
		t.Fatal("401 was not actionable needs-auth", status, count, err)
	}
	_ = s.Disconnect(context.Background(), "owned", config)
	mode.Store(2)
	value, err = s.Connect(context.Background(), "owned", config)
	if err != nil || value.Status != "connected" || len(value.Tools) != 0 || value.Failure == nil || value.Client == nil {
		t.Fatal("Source discovery failure should retain connected peer with no tools", value, err)
	}
	_ = s.Disconnect(context.Background(), "owned", config)
	mode.Store(3)
	before := lists.Load()
	status, count, err = s.Probe(context.Background(), "owned", config)
	if err != nil || status != "connected" || count != 0 || lists.Load() != before {
		t.Fatal("no tool capability should not send discovery", status, count, err, lists.Load())
	}
}
func TestDisconnectPendingInitializationCannotPublishIntoNewCacheEntry(t *testing.T) {
	var mode, initializes, lists atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	peer := loopback(t, &mode, &initializes, &lists, entered, release)
	s := manager(t, nil)
	config := parse(t, `{"type":"http","url":"`+peer.URL+`"}`)
	finished := make(chan error, 1)
	go func() { _, err := s.Connect(context.Background(), "owned", config); finished <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("initialization not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Disconnect(ctx, "owned", config); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatal("invalidated initializer should reject", err)
	}
	close(release)
	value, err := s.Connect(ctx, "owned", config)
	if err != nil || value.Status != "connected" || len(value.Tools) != 1 || initializes.Load() != 2 {
		t.Fatal("old initializer poisoned new connection", value, err, initializes.Load())
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(ctx, "owned", config); !errors.Is(err, context.Canceled) {
		t.Fatal("closed manager admitted peer", err)
	}
}
func TestConnectionIdentityIncludesResolvedConfiguration(t *testing.T) {
	s := manager(t, nil)
	a := parse(t, `{"type":"http","url":"https://owned.invalid/mcp","headers":{"Authorization":"first"},"scope":"dynamic"}`)
	b := parse(t, `{"type":"http","url":"https://owned.invalid/mcp","headers":{"Authorization":"second"},"scope":"dynamic"}`)
	if s.key("owned", a) == s.key("owned", b) || !strings.HasPrefix(s.key("owned", a), `["`) {
		t.Fatal("resolved credentials/configuration did not separate connections")
	}
}
