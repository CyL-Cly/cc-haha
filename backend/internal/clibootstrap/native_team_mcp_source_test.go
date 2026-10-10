package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualTeamNamedMCPPresetInitializedClientsToolsAndRPCResultMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		input := objMain(data)
		if input.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := json.RawMessage(`{}`)
		switch input.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-source-peer","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","description":"Actual peer","inputSchema":{"type":"object","properties":{"value":{"type":"string"}}}}]}`)
		case "tools/call":
			result = []byte(`{"content":[{"type":"text","text":"ACTUAL_SOURCE_TEAM_RPC_RESULT"}]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	names := []string{"mcp__named__echo"}
	definition := protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: rawMain([]string{"named"})})
	start := teamworker.Start{ParentSessionID: "11111111-1111-4111-8111-111111111111", TeamName: "MyTeam_", MemberID: "m", Name: "worker", SystemPrompt: "ACTUAL_APPROVED_WORKER_SYSTEM", Tools: &names, AgentDefinition: definition}
	m, f, _ := teamWorkerFixture(t, start, "http://127.0.0.1:1")
	if err := os.WriteFile(filepath.Join(f.cwd, ".mcp.json"), rawMain(map[string]any{"mcpServers": map[string]any{"named": map[string]any{"type": "http", "url": peer.URL}}}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	var actual json.RawMessage
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		prepared, err := m.prepareNativeTeamWorkerPreset(ctx, scope, names)
		if err != nil {
			return err
		}
		peers, err := m.mcpTools.AgentConnections(scope)
		if err != nil {
			return err
		}
		clients := []protocol.JSONObject{}
		for _, peer := range peers {
			clients = append(clients, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(peer.Name)}, protocol.JSONField{Name: "status", Value: quoteMain(peer.Status)}))
		}
		_, tools, err := m.mcpTools.Catalog(scope)
		if err != nil {
			return err
		}
		var selected *queryloop.RegisteredTool
		for i := range tools {
			if tools[i].Name == names[0] {
				selected = &tools[i]
			}
		}
		if selected == nil {
			return mainCap("actual configured team MCP tool")
		}
		input := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: quoteMain("actual")})
		future, err := selected.StartCallJSON(ctx, input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("actual-source-team-call"), Opaque: &builtinwrite.CallContext{Scope: scope}})
		if err != nil {
			return err
		}
		result, err := future.Await(scope)
		if err != nil {
			return err
		}
		list, err := m.Controller.TaskListIDOnOwner(scope, "worker@MyTeam_")
		if err != nil {
			return err
		}
		actual = rawMain(map[string]any{"tools": prepared.Tools, "messages": []any{}, "error": nil, "taskList": list, "clients": clients, "result": result.Data})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-worker-preset.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_TEAM_WORKER_PRESET_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ANTHROPIC_API_KEY=private-source-fixture", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": f.home, "cwd": f.cwd, "origin": peer.URL, "cases": []map[string]any{{"definition": map[string]any{"tools": names, "mcpServers": []string{"named"}}, "available": names, "call": map[string]any{"name": names[0], "input": map[string]string{"value": "actual"}}}}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(actual, &a)
	_ = json.Unmarshal(source.Rows[0], &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Go %s Source %s", actual, source.Rows[0])
	}
}
