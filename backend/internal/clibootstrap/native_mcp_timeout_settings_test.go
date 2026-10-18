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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMCPSettingsEnvironmentReloadChangesNextToolSameConnectionAndHarness(t *testing.T) {
	var initializes, tools atomic.Int32
	var mu sync.Mutex
	held := map[string]chan struct{}{}
	cancelled := make(chan struct{}, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.Get("id") == nil {
			if message.String("method") == "notifications/cancelled" {
				params, _ := message.Object("params")
				mu.Lock()
				channel := held[string(params.Get("requestId"))]
				mu.Unlock()
				if channel == nil {
					t.Error("actual SDK timeout cancellation has no owned tool request", params)
				} else {
					channel <- struct{}{}
				}
			}
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			initializes.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-reload","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"wait","inputSchema":{"type":"object","properties":{"mode":{"type":"string"}}}}]}`)
		case "tools/call":
			tools.Add(1)
			params, _ := message.Object("params")
			arguments, _ := params.Object("arguments")
			if arguments.String("mode") == "hold" {
				channel := make(chan struct{}, 1)
				mu.Lock()
				held[string(message.Get("id"))] = channel
				mu.Unlock()
				select {
				case <-channel:
					cancelled <- struct{}{}
				case <-r.Context().Done():
					return
				}
			} else {
				select {
				case <-time.After(60 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
			result = []byte(`{"content":[{"type":"text","text":"physical MCP old-window result"}]}`)
		default:
			t.Error("unexpected actual reload MCP request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		switch round := requests.Add(1); round {
		case 1:
			emitNativeTaskTool(w, "before-settings-timeout", "mcp__owned__wait", map[string]any{"mode": "fast"})
		case 2:
			if !strings.Contains(string(data), "physical MCP old-window result") {
				t.Error("initial actual MCP call did not use its longer environment window")
			}
			emitPlanFixture(w, int(round), "", "BEFORE_RELOAD_DONE")
		case 3:
			emitNativeTaskTool(w, "after-settings-timeout", "mcp__owned__wait", map[string]any{"mode": "hold"})
		case 4:
			var request protocol.JSONObject
			_ = request.UnmarshalJSON(data)
			var messages []protocol.JSONObject
			_ = json.Unmarshal(request.Get("messages"), &messages)
			found := false
			for _, message := range messages {
				for _, block := range taskBlocks(message.Get("content")) {
					if block.String("type") == "tool_result" && block.String("tool_use_id") == "after-settings-timeout" {
						found = string(block.Get("is_error")) == "true" && strings.Contains(string(block.Get("content")), `MCP server \"owned\" tool \"wait\" timed out after 0s`)
					}
				}
			}
			if !found {
				t.Error("settings reload did not reach next same Harness timeout result", string(data))
			}
			emitPlanFixture(w, int(round), "", "AFTER_RELOAD_DONE")
		default:
			t.Error("extra Harness/model request after settings timeout change", round)
		}
	}))
	defer model.Close()
	var settingsPath string
	configs := rawMain(map[string]any{"mcpServers": map[string]any{"owned": map[string]string{"type": "http", "url": peer.URL}}})
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		settingsPath = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		n.Environment["MCP_TOOL_TIMEOUT"] = "300prefix"
	}, func(o *MainPrintOptions) {
		o.Arguments.MCPConfigs = []string{string(configs)}
		o.Arguments.Tools = []string{}
	})
	originalHost := m.Host
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("Before actual settings reload")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"sample@mkt":false},"env":{"MCP_TOOL_TIMEOUT":"25suffix"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		ready := false
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(*cooperative.Scope) error {
			ready = m.nativeOpenAIEnvironment()["MCP_TOOL_TIMEOUT"] == "25suffix"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := receipt.Wait(); err != nil {
			t.Fatal(err)
		}
		return ready
	})
	if err := m.RunTurn(ctx, user("After actual settings reload")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("settings-updated SDK timeout did not cancel physical MCP request")
	}
	if initializes.Load() != 1 || tools.Load() != 2 || requests.Load() != 4 || m.Host != originalHost {
		t.Fatal("settings timeout reload recreated connection/Harness", initializes.Load(), tools.Load(), requests.Load())
	}
}
