package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOriginalCatalogKickoffOverlapsPhysicalConsumer(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	_ = os.MkdirAll(home, 0700)
	physical := filepath.Join(home, "owned-plugin-input.json")
	_ = os.WriteFile(physical, []byte(`{"owned":"actual physical plugin input"}`), 0600)
	ready := make(chan struct{})
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/mcp_servers":
			close(ready)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/control/physical-read":
			select {
			case <-ready:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(204)
		case "/control/release":
			close(release)
			w.WriteHeader(204)
		}
	}))
	defer peer.Close()
	input, _ := json.Marshal(map[string]any{"homeDir": home, "address": peer.URL, "physicalFile": physical, "tokens": map[string]any{"accessToken": "fake-owned-token", "scopes": []string{"user:mcp_servers"}}})
	fixture, _ := filepath.Abs("claudeai_catalog_lifecycle.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CLAUDEAI_CATALOG_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	if data, e := cmd.CombinedOutput(); e != nil {
		t.Fatal("actual Source kickoff", e, string(data))
	}
	// Begin admits the same physical consumer before the held HTTP response finishes.
	goReady, goReleased := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(goReady)
		select {
		case <-goReleased:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	catalog := ClaudeAICatalog{HTTP: server.Client(), BaseURL: server.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"fake-owned-token","scopes":["user:mcp_servers"]}}`), nil
	}}
	defer catalog.Close()
	consumer := make(chan []byte, 1)
	work, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	done := make(chan struct{})
	go func() {
		flight := catalog.Begin(work)
		data, _ := os.ReadFile(physical)
		consumer <- data
		_ = flight.Await(work)
		close(done)
	}()
	select {
	case <-goReady:
	case <-ctx.Done():
		t.Fatal("actual Go request absent")
	}
	select {
	case data := <-consumer:
		if string(data) != `{"owned":"actual physical plugin input"}` {
			t.Fatal("owned physical consumer read differs", string(data))
		}
	case <-time.After(50 * time.Millisecond):
		cancelWork()
		<-done
		t.Fatal("actual Source completed physical consumer before held HTTP; Go blocked it behind Configurations")
	}
	close(goReleased)
	catalog.Close()
	<-done
}
