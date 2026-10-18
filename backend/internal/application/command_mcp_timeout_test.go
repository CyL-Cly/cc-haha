package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPublicCLIMCPEnvironmentToolTimeoutReachesNextCanonicalModelPOST(t *testing.T) {
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
					t.Error("timeout cancellation did not belong to actual tool request", params)
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
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-public-timeout","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"wait","inputSchema":{"type":"object","properties":{}}}]}`)
		case "tools/call":
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
			result = []byte(`{"content":[{"type":"text","text":"late private result cannot replace timeout"}]}`)
		default:
			t.Error("unexpected actual timeout MCP request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: raw("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	var models atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		data, _ := io.ReadAll(r.Body)
		if models.Add(1) == 1 {
			response(w, "actual-timeout-call", "mcp__owned__wait", map[string]any{}, "")
			return
		}
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(data)
		var messages []protocol.JSONObject
		_ = json.Unmarshal(request.Get("messages"), &messages)
		found := false
		for _, message := range messages {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "tool_result" && strings.Contains(string(block.Get("content")), `MCP server \"owned\" tool \"wait\" timed out after 0s`) {
					found = string(block.Get("is_error")) == "true"
				}
			}
		}
		if !found || bytes.Contains(data, []byte("late private result")) || bytes.Contains(data, []byte("Request timed out")) {
			t.Error("actual Source MCP timeout did not enter next same Harness model POST", string(data))
		}
		response(w, "actual-timeout-result", "", nil, "MCP_TIMEOUT_DONE")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated Keychain unavailable")
	}
	o.Environment["MCP_TOOL_TIMEOUT"] = "25suffix"
	o.Native.Environment["MCP_TOOL_TIMEOUT"] = "25suffix"
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"owned": map[string]string{"type": "http", "url": peer.URL}}})
	command, _, err := ParseCommand([]string{"-p", "--mcp-config", string(config), "--model", "fixture-model", "--tools", "", "--thinking", "disabled", "--permission-mode", "bypassPermissions", "--max-turns", "2", "--system-prompt", "Actual MCP environment timeout consumer", "Call the owned MCP tool"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 12*time.Second)
	defer stop()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || models.Load() != 2 || !strings.Contains(output.String(), "MCP_TIMEOUT_DONE") {
		t.Fatal("public CLI timeout did not settle actual continuation", code, err, models.Load(), output.String())
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("actual SDK cancellation was not received by physical MCP peer")
	}
}
