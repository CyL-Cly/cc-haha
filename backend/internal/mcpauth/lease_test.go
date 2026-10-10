package mcpauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshLeaseStaleRecoveryReplacementOwnershipAndCancellation(t *testing.T) {
	s, _ := authFixture(t)
	p, e := s.Provider(context.Background(), "lease", record(field("type", "http"), field("url", "https://owned.example/mcp")))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.options.ConfigDir, "mcp-refresh-"+keySanitize.ReplaceAllString(p.key, "_")+".lock.lock")
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	old := s.options.Now().Add(-11 * time.Second)
	_ = os.Chtimes(path, old, old)
	release, e := p.refreshLock(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	// Another lease may replace a compromised directory. Releasing our old
	// owner must leave the replacement alive, just as proper-lockfile does.
	replaced := path + ".old"
	if e = os.Rename(path, replaced); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	release()
	release()
	if _, e = os.Stat(path); e != nil {
		t.Fatal("removed another lease", e)
	}
	_ = os.Remove(path)
	_ = os.Remove(replaced)
	release, e = p.refreshLock(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = p.refreshLock(ctx)
	if e == nil {
		t.Fatal("contended cancelled lease waited")
	}
	release()
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("owned lease leaked", e)
	}
}

func TestConcurrentTokensShareRealInFlightRefreshAndOtherProviderRereadsOwnedStore(t *testing.T) {
	s, _ := authFixture(t)
	var host *httptest.Server
	var calls atomic.Int32
	host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": host.URL, "authorization_endpoint": host.URL + "/auth", "token_endpoint": host.URL + "/token", "response_types_supported": []string{"code"}})
			return
		}
		if r.URL.Path == "/token" {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "actual-renewed", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		w.WriteHeader(404)
	}))
	defer host.Close()
	config := record(field("type", "http"), field("url", host.URL+"/mcp"))
	p, e := s.Provider(context.Background(), "race", config)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveClientInformation(context.Background(), record(field("client_id", "registered"))); e != nil {
		t.Fatal(e)
	}
	if e = p.SaveTokens(context.Background(), record(field("access_token", "old"), field("refresh_token", "freshest-refresh"), field("expires_in", 1))); e != nil {
		t.Fatal(e)
	}
	if e = p.SaveDiscoveryState(context.Background(), record(field("authorizationServerUrl", host.URL))); e != nil {
		t.Fatal(e)
	}
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			token, e := p.Token(context.Background())
			if e != nil || token != "actual-renewed" {
				t.Errorf("token=%s error=%v", token, e)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("in-flight refresh duplicated %d HTTP calls", calls.Load())
	}
	other, e := s.Provider(context.Background(), "race", config)
	if e != nil {
		t.Fatal(e)
	}
	tokens, e := other.RefreshAuthorization(context.Background(), "obsolete-refresh")
	if e != nil || tokens.String("access_token") != "actual-renewed" {
		t.Fatal(tokens, e)
	}
	if calls.Load() != 1 {
		t.Fatal("reread failed to see actual shared credentials")
	}
}
