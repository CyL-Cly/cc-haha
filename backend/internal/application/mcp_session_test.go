package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualNativeMCPToolRunsThroughSharedHarnessAndRetainsMetadata(t *testing.T) {
	var peerCalls, lists, closed atomic.Int32
	var peerInput protocol.JSONObject
	var peerMu sync.Mutex
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodDelete {
			closed.Add(1)
			w.WriteHeader(200)
			return
		}
		data, _ := io.ReadAll(r.Body)
		message := obj(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var value any
		switch message.String("method") {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "owned-peer")
			value = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "owned-peer", "version": "1"}}
		case "tools/list":
			lists.Add(1)
			value = map[string]any{"tools": []any{map[string]any{"name": "calculate", "description": "Double the input.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}, "annotations": map[string]any{"readOnlyHint": true}}}}
		case "tools/call":
			peerCalls.Add(1)
			peerMu.Lock()
			peerInput = obj(message.Get("params"))
			peerMu.Unlock()
			value = map[string]any{"content": []any{map[string]any{"type": "text", "text": " { \"answer\": 14 } "}}, "structuredContent": map[string]any{"answer": 14}, "_meta": map[string]any{"owner": "private-test"}}
		default:
			t.Errorf("undeclared peer method %s", message.String("method"))
			value = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": value}))
	}))
	defer peer.Close()
	var modelCalls atomic.Int32
	var modelMu sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		modelMu.Lock()
		requests = append(requests, data)
		modelMu.Unlock()
		if modelCalls.Add(1) == 1 {
			response(w, "mcp-native", "mcp__owned__calculate", map[string]any{"n": 7}, "")
		} else {
			response(w, "mcp-finished", "", nil, "MCP ACTUAL TOOL COMPLETE")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = []string{""}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("owned fixture unavailable Keychain")
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: raw(map[string]any{"owned": map[string]any{"type": "http", "url": peer.URL}})})
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	var init protocol.JSONObject
	o.OnSDK = func(data json.RawMessage) error {
		message := obj(data)
		if message.String("subtype") == "init" {
			init = message
		}
		return nil
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if err = session.Submit(context.Background(), tui.Input{Text: "Use owned calculate to double seven."}, nil); err != nil {
		t.Fatal(err)
	}
	if peerCalls.Load() != 1 || modelCalls.Load() != 2 || lists.Load() != 1 {
		t.Fatalf("actual shared chain did not complete: peer=%d model=%d lists=%d", peerCalls.Load(), modelCalls.Load(), lists.Load())
	}
	peerMu.Lock()
	input := peerInput.Clone()
	peerMu.Unlock()
	if input.String("name") != "calculate" || !bytes.Contains(input.Get("arguments"), []byte(`"n":7`)) || !bytes.Contains(input.Get("_meta"), []byte(`"claudecode/toolUseId":"call-mcp-native"`)) {
		t.Fatalf("actual peer call changed %s", raw(input))
	}
	if !bytes.Contains(init.Get("tools"), []byte("mcp__owned__calculate")) || !bytes.Contains(init.Get("mcp_servers"), []byte(`"status":"connected"`)) {
		t.Fatalf("init did not expose actual tools/server %s", raw(init))
	}
	modelMu.Lock()
	first, second := requests[0], requests[1]
	modelMu.Unlock()
	if !bytes.Contains(first, []byte(`"name":"mcp__owned__calculate"`)) || !bytes.Contains(first, []byte("Double the input.")) {
		t.Fatalf("actual request schema missing MCP %s", first)
	}
	if !bytes.Contains(second, []byte(`\"answer\": 14`)) || bytes.Contains(second, []byte("Structured content:")) {
		t.Fatalf("actual MCP response did not reach next inference/deduplicate %s", second)
	}
	history, _ := json.Marshal(session.process.Controller.History())
	if !bytes.Contains(history, []byte(`"mcpMeta":{"_meta":{"owner":"private-test"},"structuredContent":{"answer":14}}`)) {
		t.Fatalf("native Harness dropped original MCP metadata %s", history)
	}
	if err = session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.process.Owner.Done():
	default:
		t.Fatal("shared MCP/Harness session owner survived closure")
	}
	// The original SDK's transport.close() aborts the transport; DELETE is the
	// separate terminateSession() operation. Normal session cleanup must retain
	// that distinction.
	if closed.Load() != 0 {
		t.Fatal("cleanup unexpectedly terminated remote session", closed.Load())
	}
}
