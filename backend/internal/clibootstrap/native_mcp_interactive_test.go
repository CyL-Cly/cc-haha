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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestInteractiveMCPActualFirstTurnThenLateToolExecutionAndShutdown(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	fastReleased := make(chan struct{})
	slowStarted, slowCancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.String("method") == "initialize" {
			if r.URL.Path == "/slow" {
				close(slowStarted)
				<-r.Context().Done()
				close(slowCancelled)
				return
			}
			select {
			case <-fastReleased:
			case <-r.Context().Done():
				return
			}
		}
		if message.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-fast","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"actual_fast_tool","inputSchema":{"type":"object","properties":{}}}]}`)
		case "tools/call":
			params, _ := message.Object("params")
			if params.String("name") != "actual_fast_tool" {
				t.Error("late tool reached wrong actual MCP consumer", params)
			}
			calls.Add(1)
			result = []byte(`{"content":[{"type":"text","text":"physical fast MCP result"}]}`)
		default:
			t.Error("unexpected actual MCP request", message)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		round := int(requests.Add(1))
		switch round {
		case 1:
			if bytes.Contains(data, []byte(`"name":"mcp__fast__actual_fast_tool"`)) {
				t.Error("pending peer advertised tools before initialize completed")
			}
			emitPlanFixture(w, round, "", "First query completed while peers pending")
		case 2:
			if !bytes.Contains(data, []byte(`"name":"mcp__fast__actual_fast_tool"`)) {
				t.Error("late tool never reached canonical first-party request catalog")
			}
			emitPlanFixture(w, round, "mcp__fast__actual_fast_tool", "")
		case 3:
			if !bytes.Contains(data, []byte("physical fast MCP result")) {
				t.Error("actual MCP execution result did not reach next model POST")
			}
			emitPlanFixture(w, round, "", "Late physical MCP tool completed")
		default:
			t.Error("unexpected model continuation", round)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-interactive-fixture"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	configs := rawMain(map[string]any{"mcpServers": map[string]any{"slow": map[string]string{"type": "http", "url": peer.URL + "/slow"}, "fast": map[string]string{"type": "http", "url": peer.URL + "/fast"}}})
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Interactive: true, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{MCPConfigs: []string{string(configs)}, Model: "fixture-model", ComputerUse: &disabled, SystemPrompt: "actual interactive MCP discovery", Tools: []string{"mcp__fast__actual_fast_tool"}, PermissionMode: "bypassPermissions", MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal("pending MCP blocked interactive session startup", err)
	}
	select {
	case <-slowStarted:
	case <-ctx.Done():
		t.Fatal("actual slow MCP never started", ctx.Err())
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(text)}))})
	}
	if err := m.RunTurn(ctx, user("First pending turn")); err != nil {
		t.Fatal("pending MCP blocked first actual model query", err)
	}
	close(fastReleased)
	for {
		ready := false
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			statuses, err := m.mcpTools.Status(scope)
			for _, status := range statuses {
				ready = ready || status.String("name") == "fast" && status.String("status") == "connected"
			}
			return err
		}); err != nil {
			t.Fatal("late actual MCP connection failed", err)
		}
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.RunTurn(ctx, user("Execute connected fast tool")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || requests.Load() != 3 {
		t.Fatal("canonical late tool execution lost", calls.Load(), requests.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("interactive shutdown waited on uncancelled MCP", err)
	}
	select {
	case <-slowCancelled:
	case <-ctx.Done():
		t.Fatal("physical pending MCP not cancelled by public shutdown", ctx.Err())
	}
}

func TestInteractiveMCPHeldFakeAccountCatalogCannotHoldShutdown(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-owned-catalog","scopes":["user:mcp_servers"],"expiresAt":9999999999999},"future":true}`)
	started, cancelled := make(chan struct{}), make(chan struct{})
	proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/mcp_servers" || r.Header.Get("Authorization") != "Bearer fake-owned-catalog" {
			t.Error("unexpected fake-account catalog request", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	f.n.Environment["HTTPS_PROXY"], f.n.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
	disabled := false
	seedNativeReleaseWarmFixture(t, f, "fixture")
	m, err := NewMainPrint(MainPrintOptions{Interactive: true, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", ComputerUse: &disabled, Tools: []string{""}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, SystemPrompt: "No inference needed for actual catalog shutdown", Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal("held metadata blocked REPL startup", err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual fake-account catalog never started", ctx.Err())
	}
	closeCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer stop()
	if err := m.ShutdownSession(closeCtx); err != nil {
		t.Fatal("actual catalog native producer held interactive shutdown", err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("actual metadata HTTP socket survived ShutdownSession")
	}
}
