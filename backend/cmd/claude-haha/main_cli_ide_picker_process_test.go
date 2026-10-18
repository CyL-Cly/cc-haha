package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func ideCLIJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

func TestActualPublicGoCLIIDEPickerSameOwnerDiagnosticsNextRequestCancelAndDisconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private POSIX PTY is not native Windows UI evidence")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-ide-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, profile := range []string{"cancel", "connect"} {
		t.Run(profile, func(t *testing.T) {
			home, _ := startupFlagHome(t, root, profile, false)
			var mu sync.Mutex
			connections, toolCalls := 0, 0
			closes := make(chan struct{}, 2)
			requests := [][]byte{}
			ide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
				if e != nil {
					return
				}
				defer conn.CloseNow()
				mu.Lock()
				connections++
				mu.Unlock()
				for {
					_, data, e := conn.Read(r.Context())
					if e != nil {
						closes <- struct{}{}
						return
					}
					var msg protocol.JSONObject
					_ = msg.UnmarshalJSON(data)
					if msg.Get("id") == nil {
						continue
					}
					result := json.RawMessage(`{}`)
					switch msg.String("method") {
					case "initialize":
						result = []byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"private-public-cli-ide","version":"1"}}`)
					case "tools/list":
						result = []byte(`{"tools":[{"name":"getDiagnostics","description":"Actual private diagnostics","inputSchema":{"type":"object"}}]}`)
					case "tools/call":
						mu.Lock()
						toolCalls++
						mu.Unlock()
						result = []byte(`{"content":[{"type":"text","text":"PUBLIC_CLI_IDE_PHYSICAL_ACTION"}]}`)
					}
					reply := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: ideCLIJSON("2.0")}, protocol.JSONField{Name: "id", Value: msg.Get("id")}, protocol.JSONField{Name: "result", Value: result})
					data, _ = reply.MarshalJSON()
					if e := conn.Write(r.Context(), websocket.MessageText, data); e != nil {
						return
					}
				}
			}))
			defer ide.Close()
			port := strings.TrimPrefix(ide.URL, "http://127.0.0.1:")
			lock := filepath.Join(home, ".claude", "ide", port+".lock")
			_ = os.MkdirAll(filepath.Dir(lock), 0700)
			if e := os.WriteFile(lock, ideCLIJSON(map[string]any{"workspaceFolders": []string{home}, "transport": "ws", "ideName": "Private CLI IDE", "authToken": "private-ide-token"}), 0600); e != nil {
				t.Fatal(e)
			}
			model := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, append([]byte{}, data...))
				index := len(requests)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(name string, value any) {
					data, _ := json.Marshal(value)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
				}
				emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprint("ide-", index), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
				stop := "end_turn"
				block := map[string]any{"type": "text", "text": ""}
				delta := map[string]string{"type": "text_delta", "text": "PUBLIC_IDE_TOOL_CONTINUED"}
				if index == 1 {
					if !bytes.Contains(data, []byte("mcp__ide__getDiagnostics")) {
						t.Error("same public CLI request did not include connected IDE tool")
					}
					stop = "tool_use"
					block = map[string]any{"type": "tool_use", "id": "public-ide-call", "name": "mcp__ide__getDiagnostics", "input": map[string]any{}}
					delta = map[string]string{"type": "input_json_delta", "partial_json": "{}"}
				} else if !bytes.Contains(data, []byte("PUBLIC_CLI_IDE_PHYSICAL_ACTION")) || bytes.Contains(data, []byte(`"is_error":true`)) {
					t.Error("actual tool failed or missing from next model request", string(data))
				}
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta})
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
				emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
				emit("message_stop", map[string]string{"type": "message_stop"})
			}))
			defer model.Close()
			flags, _ := json.Marshal([]string{"--bare", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--tools", "Read", "--allowedTools", "mcp__ide__getDiagnostics", "--max-turns", "4"})
			python := exec.CommandContext(ctx, "/usr/bin/python3", "testdata/ide-picker.py", binary, home, profile, string(flags))
			python.Env = append(resumeErrorEnv(home, root, model.URL), "TERM=xterm-256color", "CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL=1")
			capturePublicGoCoverage(t, python)
			out, e := python.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			if !bytes.Contains(out, []byte(`"restored": true`)) {
				t.Fatal("actual public IDE terminal restoration missing", string(out))
			}
			mu.Lock()
			count, tools, models := connections, toolCalls, len(requests)
			mu.Unlock()
			if profile == "cancel" {
				if count != 0 || tools != 0 || models != 0 {
					t.Fatal("cancel started real IDE/model activity", count, tools, models)
				}
			} else {
				if count != 1 || tools != 1 || models != 2 {
					t.Fatal("actual public IDE graph mismatch", count, tools, models)
				}
				select {
				case <-closes:
				case <-ctx.Done():
					t.Fatal("public UI disconnect did not close IDE socket")
				}
			}
		})
	}
}
