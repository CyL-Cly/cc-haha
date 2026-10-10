package clibootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestClaudeMCPSharedRefreshWaiterCancelAndCloseJoinHeldPhysicalSocket(t *testing.T) {
	m := authCommandFixture(t, map[string]any{"env": map[string]string{}, "global": map[string]any{"oauthAccount": map[string]any{"billingType": "owned", "accountCreatedAt": "owned", "subscriptionCreatedAt": "owned"}}})
	m.Native.NowMillis = func() float64 { return 1000000 }
	m.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
	path := filepath.Join(m.Native.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json")
	payload := `{"claudeAiOauth":{"accessToken":"fake-old","refreshToken":"fake-refresh","expiresAt":17,"scopes":["user:inference"],"subscriptionType":"pro","rateLimitTier":"owned-tier"},"future":true}`
	agentsWrite(t, path, payload)
	started, closed := make(chan struct{}), make(chan struct{})
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/oauth/token" {
			t.Error("unexpected refresh request", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = io.ReadAll(r.Body)
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	m.Native.Environment["HTTPS_PROXY"], m.Native.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
	binding, err := m.NewCLIClaudeAIMCP(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- binding.refresh.check(firstCtx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("physical refresh not started")
	}
	cancel()
	select {
	case err = <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiter cancellation lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter not released")
	}
	select {
	case <-closed:
		t.Fatal("individual waiter cancelled shared refresh producer")
	default:
	}
	second := make(chan error, 1)
	go func() { second <- binding.refresh.check(context.Background()) }()
	joined := make(chan struct{})
	go func() { binding.Close(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("owned refresh Close failed to join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("physical token socket survived Close")
	}
	select {
	case err = <-second:
		if err == nil {
			t.Fatal("closed producer accepted new waiter")
		}
	case <-time.After(time.Second):
		t.Fatal("shared waiter survived Close")
	}
	data, _ := os.ReadFile(path)
	if string(data) != payload {
		t.Fatal("cancelled refresh changed credential bytes")
	}
}

func TestClaudeMCPOrdinaryIndependentAgentBorrowsProcessMemoAndChildClosePreservesParent(t *testing.T) {
	parent := backgroundAgentMain(t, "http://127.0.0.1:1")
	if parent.claudeAIMCP == nil {
		t.Fatal("actual native parent MCP producer missing")
	}
	var options MainPrintOptions
	err := parent.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, parent, "No model request is needed for this owned lifecycle test")
		if err != nil {
			return err
		}
		options, err = parent.nativeIndependentAgentOptions(scope, request, "a-owned-catalog", parent.options.Native.CWD, "fixture-model", filepath.Join(parent.options.Native.Temp, "owned-agent-catalog"), request.SystemPromptSections, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err = child.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if child.mcpRuntime != parent.mcpRuntime || !child.mcpBorrowed {
		t.Fatal("ordinary Agent did not inherit actual parent MCP process graph")
	}
	if child.claudeAIMCP != parent.claudeAIMCP || !child.claudeAIMCPBorrowed {
		t.Fatal("ordinary child created another process catalog memo")
	}
	if err = child.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	child.Close()
	if err = parent.claudeAIMCP.refresh.check(context.Background()); err != nil {
		t.Fatal("child closed parent's shared refresh producer", err)
	}
	// The parent must still accept its actual all-config consumer after the
	// independent child's runtime and native resources have fully closed.
	if _, err = parent.claudeAIMCP.All(context.Background(), parent.mcpConfigurations); err != nil {
		t.Fatal("child closed parent producer", err)
	}
}

func TestClaudeMCPAllActualNativePluginFilesOverlapHeldCatalogAndOrganizationDedup(t *testing.T) {
	m := authCommandFixture(t, map[string]any{"env": map[string]string{}, "settings": map[string]any{"enabledPlugins": map[string]any{"alpha@owned": true}}})
	m.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
	config := m.Native.Environment["CLAUDE_CONFIG_DIR"]
	agentsWrite(t, filepath.Join(config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-catalog","scopes":["user:mcp_servers"],"expiresAt":9999999999999}}`)
	market := filepath.Join(m.Native.Home, "market")
	plugin := filepath.Join(market, "alpha")
	agentsWrite(t, filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"owned","owner":{"name":"Owned"},"plugins":[{"name":"alpha","source":"./alpha"}]}`)
	agentsWrite(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0.0"}`)
	mcpFile := filepath.Join(plugin, ".mcp.json")
	agentsWrite(t, mcpFile, `{"owned":{"type":"http","url":"https://vendor.invalid/shared"}}`)
	agentsWrite(t, filepath.Join(config, "plugins", "known_marketplaces.json"), string(rawMain(map[string]any{"owned": map[string]any{"source": map[string]any{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}})))
	m.Settings.InvalidateSettingsCache()
	loaded, err := m.Settings.RefreshPlugins(context.Background(), false)
	if err != nil || len(loaded.Enabled) != 1 || loaded.Enabled[0].Name != "alpha" {
		t.Fatal("fixture did not actually load owned plugin", err, loaded)
	}
	opts, err := m.Settings.PluginOptions(m.Secure)
	if err != nil {
		t.Fatal(err)
	}
	plugins, _, err := m.Settings.PluginMCP(opts, m.Secure)
	if err != nil {
		t.Fatal(err)
	}
	configurations, err := m.Settings.MCPConfigurations(plugins, false)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/mcp_servers" {
			t.Error("unexpected catalog request", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"owned","display_name":"Owned","url":"https://vendor.invalid/shared"}]}`)
	}))
	m.Native.Environment["HTTPS_PROXY"], m.Native.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
	binding, err := m.NewCLIClaudeAIMCP(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Native.mu.Lock()
	m.Native.Trace = nil
	m.Native.mu.Unlock()
	result := make(chan protocol.JSONObject, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := binding.All(context.Background(), configurations)
		if err != nil {
			failure <- err
			return
		}
		result <- value.Servers
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("actual catalog producer not started")
	}
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	read := false
	for !read {
		snapshot, err := m.Settings.LoadPlugins(context.Background(), "", false)
		if err != nil {
			binding.Close()
			t.Fatal(err)
		}
		for _, item := range snapshot.Enabled {
			if item.Name == "alpha" && item.MCPServers != nil {
				var value protocol.JSONObject
				if value.UnmarshalJSON(item.MCPServers.Get("owned")) == nil && value.String("url") == "https://vendor.invalid/shared" {
					read = true
				}
			}
		}
		if read {
			break
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			binding.Close()
			t.Fatal("actual native plugin MCP physical file was not published while catalog held", snapshot)
		}
	}
	select {
	case <-result:
		t.Fatal("all-config result settled before shared catalog")
	default:
	}
	close(release)
	select {
	case value := <-result:
		if value.Get("claude.ai Owned") == nil || len(value.OwnProperties()) != 1 {
			t.Fatal("actual organization did not suppress duplicate plugin", string(rawMain(value)))
		}
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("all configurations did not complete")
	}
	binding.Close()
}
