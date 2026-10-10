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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualMainPrintToolSearchDiscoversSamePhysicalMCPToolThenCallsIt(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["ENABLE_TOOL_SEARCH"] = "true"
	var mu sync.Mutex
	counts := map[string]int{}
	requests := []protocol.JSONObject{}
	headers := []http.Header{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		_ = in.UnmarshalJSON(data)
		if in.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mu.Lock()
		counts[in.String("method")]++
		mu.Unlock()
		var result json.RawMessage
		switch in.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-tool-search-peer","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","description":"Echo the provided text using the original connection","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]},"annotations":{"readOnlyHint":true}}]}`)
		case "tools/call":
			params, _ := in.Object("params")
			args, _ := params.Object("arguments")
			result = rawMain(map[string]any{"content": []map[string]string{{"type": "text", "text": "physical peer received: " + args.String("text")}}})
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: in.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(peer.Close)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var in protocol.JSONObject
		_ = in.UnmarshalJSON(data)
		mu.Lock()
		requests = append(requests, in)
		headers = append(headers, r.Header.Clone())
		index := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, rawMain(value)) }
		emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("tool-search-%d", index), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
		stop := "end_turn"
		if index <= 2 {
			name, id, text := "ToolSearch", "search-actual", `{"query":"select:mcp__fixture__echo"}`
			if index == 2 {
				name, id, text = "mcp__fixture__echo", "call-actual", `{"text":"actual harness tool search"}`
			}
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": text}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			stop = "tool_use"
		} else {
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "Used the actual discovered tool"}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		}
		emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
		emit("message_stop", map[string]string{"type": "message_stop"})
	}))
	t.Cleanup(model.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "real original tool search followed by original MCP execution", Tools: []string{"ToolSearch,mcp__fixture__echo"}, PermissionMode: "bypassPermissions", MaxTurns: 5, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Find and call the actual MCP echo tool")}))})
	if e = m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || counts["initialize"] != 1 || counts["tools/list"] != 1 || counts["tools/call"] != 1 {
		t.Fatal("actual ToolSearch/MCP continuation missing", len(requests), counts)
	}
	for i, request := range requests {
		var tools []protocol.JSONObject
		_ = json.Unmarshal(request.Get("tools"), &tools)
		found := false
		for _, tool := range tools {
			if tool.String("name") == "mcp__fixture__echo" {
				found = true
				if string(tool.Get("defer_loading")) != "true" {
					t.Error("discovered MCP definition did not retain original defer_loading", i)
				}
			}
		}
		if found != (i > 0) {
			t.Error("actual schema discovery changed", i, found)
		}
		if !strings.Contains(headers[i].Get("anthropic-beta"), "advanced-tool-use-2025-11-20") {
			t.Error("actual provider request omitted original ToolSearch beta", i, headers[i].Get("anthropic-beta"))
		}
		if !bytesContainsSearchHeader(request.Get("messages")) {
			t.Error("original deferred names header missing", i)
		}
	}
	if !strings.Contains(string(rawMain(requests[1])), `tool_reference`) || !strings.Contains(string(rawMain(requests[2])), "physical peer received: actual harness tool search") {
		t.Fatal("search reference or actual MCP result did not reach next physical POST")
	}
}

func bytesContainsSearchHeader(messages json.RawMessage) bool {
	return strings.Contains(string(messages), "<available-deferred-tools>") && strings.Contains(string(messages), "mcp__fixture__echo")
}
