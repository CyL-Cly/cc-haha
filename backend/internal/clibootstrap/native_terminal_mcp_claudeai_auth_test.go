package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func newTerminalConnectorModel(t *testing.T, models *atomic.Int32, tool string) string {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(body)
		count := models.Add(1)
		present := bytes.Contains(message.Get("tools"), quoteMain(tool))
		if present != (count <= 3) {
			t.Error("actual next connector catalog", count, present, string(message.Get("tools")))
		}
		switch count {
		case 1:
			emitPlanFixture(w, int(count), tool, "")
		case 2:
			if !bytes.Contains(body, []byte("actual private ClaudeAI proxy tool result")) {
				t.Error("actual proxy tool result did not reach next HTTP")
			}
			emitPlanFixture(w, int(count), "", "Proxy tool completed")
		case 3:
			emitPlanFixture(w, int(count), "", "Cancelled clear retained peer")
		case 4:
			emitPlanFixture(w, int(count), "", "Confirmed clear removed peer")
		default:
			t.Error("unexpected connector model request", count)
		}
	}))
	t.Cleanup(peer.Close)
	return peer.URL
}

func TestTerminalClaudeAIConnectorActualCatalogBrowserReconnectClearAndNextHarness(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.config, ".credentials.json"), `{"future":{"keep":true},"claudeAiOauth":{"accessToken":"private-cached-connector-account","scopes":["user:mcp_servers","user:inference"],"expiresAt":9999999999999}}`)
	f.write(t, filepath.Join(f.config, ".claude.json"), `{"oauthAccount":{"organizationUuid":"private-org","accountUuid":"private-account"}}`)
	browser := filepath.Join(f.home, "private-browser")
	f.write(t, browser, "#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n")
	if err := os.Chmod(browser, 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["BROWSER"], f.n.Environment["AUTH_BROWSER_RECEIPT"] = browser, filepath.Join(f.home, "browser-url")
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
	var authorized atomic.Bool
	var catalogs, initializes, calls, models atomic.Int32
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-cached-connector-account" {
			t.Error("proxy lost actual cached-account credential", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Host == "api.anthropic.com" && r.URL.Path == "/v1/mcp_servers" && r.Method == "GET" {
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"display_name":"Owned","id":"mcprs-owned","url":"https://owned-connector.invalid/mcp"}]}`)
			return
		}
		if r.Host != "mcp-proxy.anthropic.com" || r.URL.Path != "/v1/mcp/mcprs-owned" {
			t.Error("unowned official endpoint (including OAuth refresh)", r.Host, r.URL)
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !authorized.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(body)
		if message.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			initializes.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"private-connector","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"inspect","inputSchema":{"type":"object","properties":{}}}]}`)
		case "tools/call":
			calls.Add(1)
			result = []byte(`{"content":[{"type":"text","text":"actual private ClaudeAI proxy tool result"}]}`)
		default:
			t.Error("unexpected actual proxy RPC", message.String("method"))
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	f.n.Environment["HTTPS_PROXY"], f.n.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
	seedNativeReleaseWarmFixture(t, f, "fixture")
	tool := mcpclient.ToolName("claude.ai Owned", "inspect")
	model := newTerminalConnectorModel(t, &models, tool)
	f.n.Environment["ANTHROPIC_BASE_URL"] = model
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Interactive: true, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", ComputerUse: &disabled, SystemPrompt: "Actual connector browser and same Harness", Tools: []string{tool}, PermissionMode: "bypassPermissions", MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		value := ""
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			rows, err := m.mcpTools.Status(scope)
			for _, row := range rows {
				if row.String("name") == "claude.ai Owned" {
					value = row.String("status")
				}
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for value := status(); value == "" || value == "pending"; value = status() {
		select {
		case <-ctx.Done():
			t.Fatal("actual catalog never published connector")
		case <-time.After(time.Millisecond):
		}
	}
	if value := status(); value != "needs-auth" {
		t.Fatal("actual unauthenticated connector state", value)
	}
	address, err := m.TerminalMCPClaudeAIAuthURL(ctx, "claude.ai Owned", false)
	if err != nil || address != "https://claude.ai/api/organizations/private-org/mcp/start-auth/mcpsrv-owned?product_surface=cli" {
		t.Fatal("actual cached account URL", address, err)
	}
	if opened, err := m.OpenTerminalPluginLink(ctx, address); err != nil || !opened {
		t.Fatal("actual private connector opener", opened, err)
	}
	if value, err := os.ReadFile(filepath.Join(f.home, "browser-url")); err != nil || string(value) != address {
		t.Fatal("browser did not receive exact original URL", string(value), err)
	}
	authorized.Store(true)
	if err := m.CompleteTerminalMCPClaudeAIAuthentication(ctx, "claude.ai Owned", false); err != nil {
		t.Fatal(err)
	}
	if value := status(); value != "connected" {
		t.Fatal("browser completion did not actually reconnect", value)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(text)}))})
	}
	if err := m.RunTurn(ctx, user("Use the actual connected proxy tool")); err != nil {
		t.Fatal(err)
	}
	clearURL, err := m.TerminalMCPClaudeAIAuthURL(ctx, "claude.ai Owned", true)
	if err != nil || clearURL != "https://claude.ai/settings/connectors" {
		t.Fatal(clearURL, err)
	}
	// Source first clear Enter only opens a browser. Escape at that point does
	// not revoke the account, disconnect a peer, or change the next tool catalog.
	if opened, err := m.OpenTerminalPluginLink(ctx, clearURL); err != nil || !opened {
		t.Fatal(opened, err)
	}
	if err := m.RunTurn(ctx, user("Continue after cancelling connector clear")); err != nil {
		t.Fatal(err)
	}
	if err := m.CompleteTerminalMCPClaudeAIAuthentication(ctx, "claude.ai Owned", true); err != nil {
		t.Fatal(err)
	}
	if value := status(); value != "needs-auth" {
		t.Fatal("confirmed clear did not project Source needs-auth", value)
	}
	if err := m.RunTurn(ctx, user("Continue after confirmed connector clear")); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(f.config, ".credentials.json"))
	if err != nil || !bytes.Contains(stored, []byte("private-cached-connector-account")) || !bytes.Contains(stored, []byte(`"keep":true`)) || catalogs.Load() != 1 || initializes.Load() != 1 || calls.Load() != 1 || models.Load() != 4 {
		t.Fatal("connector lifecycle changed cached account or physical consumer counts", string(stored), catalogs.Load(), initializes.Load(), calls.Load(), models.Load(), err)
	}
}
