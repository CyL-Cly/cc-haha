package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeMCPHookUsesSameConnectionModifiesInputAndReplacesModelOutput(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var x protocol.JSONObject
		_ = x.UnmarshalJSON(body)
		if x.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch x.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"native-hook-peer","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","description":"Actual private MCP tool","inputSchema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}]}`)
		case "tools/call":
			calls.Add(1)
			params, _ := x.Object("params")
			args, _ := params.Object("arguments")
			if params.String("name") != "echo" || args.String("value") != "hook-selected-input" {
				t.Errorf("actual shared peer got wrong tool input %s", body)
			}
			result = []byte(`{"content":[{"type":"text","text":"ORIGINAL_PRIVATE_MCP_OUTPUT"}],"_meta":{"future":{"keep":true}}}`)
		default:
			t.Errorf("unexpected physical MCP method %s", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: x.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	var posts atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := posts.Add(1)
		if index == 1 {
			emitNativeTaskTool(w, "mcp-hook-use", "mcp__fixture__echo", map[string]any{"value": "original-model-input"})
			return
		}
		if strings.Contains(string(body), "ORIGINAL_PRIVATE_MCP_OUTPUT") || !strings.Contains(string(body), "HOOK_REPLACED_MCP_OUTPUT") || !strings.Contains(string(body), "post-mcp-context") {
			t.Errorf("Source MCP replacement did not reach actual subsequent model request %s", body)
		}
		emitPlanFixture(w, int(index), "", "MCP_HOOK_DONE")
	}))
	defer model.Close()
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}}), 0600); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(n.CWD, "mcp-hooks.py")
		code := fmt.Sprintf("import sys,json\nx=json.load(sys.stdin)\ne=x['hook_event_name']\ns={'hookEventName':e}\nif e=='PreToolUse':s.update({'permissionDecision':'allow','updatedInput':{'value':'hook-selected-input'}})\nelse:\n assert 'ORIGINAL_PRIVATE_MCP_OUTPUT' in json.dumps(x['tool_response'])\n s.update({'additionalContext':'post-mcp-context','updatedMCPToolOutput':[{'type':'text','text':'HOOK_REPLACED_MCP_OUTPUT'}]})\nprint(json.dumps({'hookSpecificOutput':s}))\n")
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		hooks := map[string]any{}
		for _, event := range []string{"PreToolUse", "PostToolUse"} {
			hooks[event] = []any{map[string]any{"matcher": "mcp__fixture__echo", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script)}}}}
		}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": hooks}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,mcp__fixture__echo"}
		o.Arguments.PermissionMode = "default"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual MCP Hook call"})})); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || posts.Load() != 2 {
		t.Fatal("actual shared MCP/model execution counts", calls.Load(), posts.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
