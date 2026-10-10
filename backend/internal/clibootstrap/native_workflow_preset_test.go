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
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowCustomAgentSkillAndMCPNativeLifecycle(t *testing.T) {
	var sharedInit, inlineInit, inlineClose, sharedClose, sharedCalls, inlineCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inline := r.URL.Path == "/inline"
		if r.Method == "GET" {
			w.WriteHeader(405)
			return
		}
		if r.Method == "DELETE" {
			if inline {
				inlineClose.Add(1)
			} else {
				sharedClose.Add(1)
			}
			w.WriteHeader(200)
			return
		}
		var in protocol.JSONObject
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			w.WriteHeader(400)
			return
		}
		if in.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := protocol.NewJSONObject()
		switch in.String("method") {
		case "initialize":
			if inline {
				inlineInit.Add(1)
			} else {
				sharedInit.Add(1)
			}
			w.Header().Set("Mcp-Session-Id", "owned-"+r.URL.Path)
			_ = result.UnmarshalJSON([]byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"private-preset-peer","version":"1"}}`))
		case "tools/list":
			_ = result.UnmarshalJSON([]byte(`{"tools":[{"name":"echo","description":"Physical peer echo","annotations":{"readOnlyHint":true},"inputSchema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}]}`))
		case "tools/call":
			if inline {
				inlineCalls.Add(1)
			} else {
				sharedCalls.Add(1)
			}
			params, _ := in.Object("params")
			args, _ := params.Object("arguments")
			result.Set("content", rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain("physical-peer:" + args.String("value"))})}))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: in.Get("id")}, protocol.JSONField{Name: "result", Value: rawMain(result)}))
	}))
	defer peer.Close()
	var parent *MainPrint
	var inlineClient *mcpclient.Client
	var modelCalls atomic.Int32
	var output string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "PRESET_SKILL_CONTENT") || !strings.Contains(string(body), "<command-message>prime</command-message>") || !strings.Contains(string(body), "<skill-format>true</skill-format>") {
			t.Errorf("actual Source skill prelude missing %.1700s", body)
		}
		switch n := modelCalls.Add(1); n {
		case 1:
			emitNativeTaskTool(w, "preset-shared", "mcp__shared__echo", map[string]any{"value": "shared"})
		case 2:
			if !strings.Contains(string(body), "physical-peer:shared") {
				t.Error("inherited actual MCP result missing")
			}
			emitNativeTaskTool(w, "preset-inline", "mcp__inline__echo", map[string]any{"value": "inline"})
		case 3:
			config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("http")}, protocol.JSONField{Name: "url", Value: quoteMain(peer.URL + "/inline")}, protocol.JSONField{Name: "scope", Value: quoteMain("dynamic")})
			captured, e := parent.mcpRuntime.Connect(context.Background(), "inline", config)
			if e != nil {
				t.Error(e)
			} else {
				inlineClient = captured.Client
			}
			if !strings.Contains(string(body), "physical-peer:inline") {
				t.Errorf("inline actual MCP result missing %.1800s", body)
			}
			emitNativeTaskTool(w, "preset-write", "Write", map[string]any{"file_path": output, "content": "ACTUAL_PRESET_FILE"})
		case 4:
			emitNativeTaskTool(w, "preset-read", "Read", map[string]any{"file_path": output})
		default:
			if !strings.Contains(string(body), "ACTUAL_PRESET_FILE") {
				t.Errorf("actual preset file result missing %.1800s", body)
			}
			emitPlanFixture(w, int(n), "", "ACTUAL_PRESET_OK")
		}
	}))
	defer model.Close()
	m := backgroundAgentMainWithSetup(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		_ = os.MkdirAll(filepath.Join(n.CWD, ".claude", "agents"), 0700)
		_ = os.MkdirAll(filepath.Join(n.CWD, ".claude", "skills", "prime"), 0700)
		_ = os.WriteFile(filepath.Join(n.CWD, ".claude", "skills", "prime", "SKILL.md"), []byte("---\ndescription: Actual preload\nmodel: different-unused-model\neffort: low\nallowed-tools: Bash\n---\nPRESET_SKILL_CONTENT"), 0600)
		agent := fmt.Sprintf("---\nname: preset-worker\ndescription: actual native preset\ntools: Read, Write, mcp__shared__echo, mcp__inline__echo\nskills: [prime]\nmcpServers:\n  - shared\n  - inline:\n      type: http\n      url: %s/inline\n---\nExecute the actual worker task.", peer.URL)
		_ = os.WriteFile(filepath.Join(n.CWD, ".claude", "agents", "preset.md"), []byte(agent), 0600)
		_ = os.WriteFile(filepath.Join(n.CWD, ".mcp.json"), []byte(fmt.Sprintf(`{"mcpServers":{"shared":{"type":"http","url":%q}}}`, peer.URL+"/shared")), 0600)
		_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["mcp__shared__echo","mcp__inline__echo"]}}`), 0600)
	})
	parent = m
	output = filepath.Join(m.options.Native.CWD, "preset-proof.txt")
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		result, e := m.agents.List(scope)
		if e == nil && len(result.FailedFiles) > 0 {
			t.Logf("actual Agent discovery failures %+v", result.FailedFiles)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`return await agent('Use both actual peers then Write and Read',{agentType:'preset-worker'})`)))
	if e != nil {
		t.Fatal(e)
	}
	result, e := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_preset123", WorkflowName: "preset", Model: "fixture-model", Now: m.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: m.runNativeWorkflowAgent, Emit: func(protocol.JSONObject) error { return nil }})
	if e != nil || result.Error != "" || string(result.Result) != `"ACTUAL_PRESET_OK"` || modelCalls.Load() != 5 {
		t.Fatalf("actual preset failed %+v %v models%d peers%d/%d close%d/%d", result, e, modelCalls.Load(), sharedInit.Load(), inlineInit.Load(), sharedClose.Load(), inlineClose.Load())
	}
	bytes, e := os.ReadFile(output)
	if e != nil || string(bytes) != "ACTUAL_PRESET_FILE" {
		t.Fatal(e, string(bytes))
	}
	if sharedInit.Load() != 1 || inlineInit.Load() != 1 || sharedCalls.Load() != 1 || inlineCalls.Load() != 1 || inlineClose.Load() != 0 || sharedClose.Load() != 0 {
		t.Fatal("actual Source peer lifecycle differs", sharedInit.Load(), inlineInit.Load(), sharedCalls.Load(), inlineCalls.Load(), sharedClose.Load(), inlineClose.Load())
	}
	if inlineClient == nil {
		t.Fatal("physical inline client was not observed")
	}
	if _, e := inlineClient.CallToolJSON(context.Background(), quoteMain("echo"), protocol.NewJSONObject(), protocol.NewJSONObject(), mcpclient.RequestOptions{}); e == nil {
		t.Fatal("inline cleanup left its actual transport open")
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if sharedClose.Load() != 0 {
		t.Fatal("client.close must not terminate the server session", sharedClose.Load())
	}
}
