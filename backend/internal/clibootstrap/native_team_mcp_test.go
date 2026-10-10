package clibootstrap

import (
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
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualTeamNamedMCPPresetSharesRealInitializedPeerAndExecutesAcrossTurns(t *testing.T) {
	var initialize, calls, models atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		if err := input.UnmarshalJSON(data); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if input.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := json.RawMessage(`{}`)
		switch input.String("method") {
		case "initialize":
			initialize.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-team-peer","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","description":"Actual peer","inputSchema":{"type":"object","properties":{"value":{"type":"string"}}},"annotations":{"readOnlyHint":true}}]}`)
		case "tools/call":
			calls.Add(1)
			params := objMain(input.Get("params"))
			if params.String("name") != "echo" || objMain(params.Get("arguments")).String("value") != "actual team RPC" {
				t.Errorf("wrong real RPC %s", data)
			}
			result = []byte(`{"content":[{"type":"text","text":"ACTUAL_TEAM_MCP_RESULT"}]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		round := models.Add(1)
		if round == 1 {
			if !strings.Contains(string(data), "mcp__named__echo") {
				t.Error("named preset real schema missing")
			}
			emitWorktreeModel(w, 1, "mcp__named__echo", `{"value":"actual team RPC"}`, "")
		} else {
			if round == 2 && !strings.Contains(string(data), "ACTUAL_TEAM_MCP_RESULT") {
				t.Error("real peer result not sent to model")
			}
			emitWorktreeModel(w, int(round), "", "", "Actual team MCP complete")
		}
	}))
	defer provider.Close()
	names := []string{"mcp__named__echo"}
	definition := protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: rawMain([]string{"named"})})
	start := teamworker.Start{ParentSessionID: "11111111-1111-4111-8111-111111111111", TeamName: "mcp-team", MemberID: "member", Name: "worker", SystemPrompt: "Approved actual MCP worker", Tools: &names, AgentDefinition: definition}
	m, f, _ := teamWorkerFixture(t, start, provider.URL)
	if err := os.WriteFile(filepath.Join(f.cwd, ".mcp.json"), rawMain(map[string]any{"mcpServers": map[string]any{"named": map[string]any{"type": "http", "url": peer.URL}}}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"Perform the approved real MCP task", "Continue same worker"} {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": prompt})})
		if err := m.RunTurn(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if initialize.Load() != 1 || calls.Load() != 1 || models.Load() != 3 {
		t.Fatal("shared peer/lazy preset lifecycle changed", initialize.Load(), calls.Load(), models.Load())
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		peers, err := m.mcpTools.AgentConnections(scope)
		if err != nil {
			return err
		}
		if len(peers) != 2 || peers[0].Client != peers[1].Client {
			t.Error("actual shared named client merge not retained", len(peers))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func objMain(data json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(data)
	return value
}
