package clibootstrap

import (
	"bytes"
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

func TestMainPrintConcurrentResourceToolsUseSameConnectionPersistBlobAndReachNextPOST(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	var mu sync.Mutex
	counts := map[string]int{}
	requests := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		_ = input.UnmarshalJSON(data)
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mu.Lock()
		counts[input.String("method")]++
		mu.Unlock()
		var result json.RawMessage
		switch input.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}},"serverInfo":{"name":"private-resource-harness","version":"1"}}`)
		case "resources/list":
			result = []byte(`{"resources":[{"uri":"scheme:binary","name":"Actual binary"}]}`)
		case "resources/read":
			result = []byte(`{"contents":[{"uri":"scheme:binary","mimeType":"application/octet-stream","blob":"AAE/Cg=="}]}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(peer.Close)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(data)
		mu.Lock()
		requests = append(requests, input)
		index := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, rawMain(value)) }
		emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("resources-%d", index), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
		stop := "end_turn"
		if index == 1 {
			for i, call := range []struct {
				name, id, input string
			}{{"ListMcpResourcesTool", "list-resources", `{}`}, {"ReadMcpResourceTool", "read-resource", `{"server":"fixture","uri":"scheme:binary"}`}} {
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": call.id, "name": call.name, "input": map[string]any{}}})
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]string{"type": "input_json_delta", "partial_json": call.input}})
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
			}
			stop = "tool_use"
		} else {
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "Actual MCP resources processed"}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		}
		emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
		emit("message_stop", map[string]string{"type": "message_stop"})
	}))
	t.Cleanup(model.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "same actual SDK readonly resource tools", Tools: []string{"ListMcpResourcesTool,ReadMcpResourceTool"}, MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("List and read the actual private MCP resource")}))})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || counts["initialize"] != 1 || counts["resources/list"] != 1 || counts["resources/read"] != 1 {
		t.Fatal("actual shared query continuation/connection/cache changed", len(requests), counts)
	}
	var body struct{ Messages []protocol.JSONObject }
	_ = json.Unmarshal(rawMain(requests[1]), &body)
	path := ""
	seen := map[string]bool{}
	for _, message := range body.Messages {
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") != "tool_result" {
				continue
			}
			seen[block.String("tool_use_id")] = true
			if block.String("tool_use_id") == "read-resource" {
				var result struct{ Contents []protocol.JSONObject }
				if e := json.Unmarshal([]byte(block.String("content")), &result); e != nil || len(result.Contents) != 1 {
					t.Fatal("actual projected resource result lost", e, block)
				}
				path = result.Contents[0].String("blobSavedTo")
			}
		}
	}
	data, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(data, []byte{0, 1, 63, 10}) || !seen["list-resources"] || !seen["read-resource"] || !strings.Contains(path, m.Controller.SessionID()) {
		t.Fatal("real resource tool result/physical binary/SID not preserved", seen, path, e)
	}
	if bytes.Contains(rawMain(requests[1]), []byte("AAE/Cg==")) {
		t.Fatal("raw binary base64 leaked into model request")
	}
}
