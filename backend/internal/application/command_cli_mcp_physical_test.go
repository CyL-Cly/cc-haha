package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicCLIDynamicMCPOverridesFilesStrictAndBareThenColdResumes(t *testing.T) {
	for _, mode := range []string{"normal", "strict", "bare"} {
		t.Run(mode, func(t *testing.T) {
			var dynamicCalls, staticLists, modelCalls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				data, _ := io.ReadAll(r.Body)
				message := obj(data)
				if message.Get("id") == nil {
					w.WriteHeader(202)
					return
				}
				var result any
				switch message.String("method") {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "dynamic", "version": "1"}}
				case "tools/list":
					if r.URL.Path == "/static" {
						staticLists.Add(1)
					}
					result = map[string]any{"tools": []any{map[string]any{"name": "calculate", "description": "Private dynamic task", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}, "annotations": map[string]any{"readOnlyHint": true}}}}
				case "tools/call":
					dynamicCalls.Add(1)
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "MCP_DYNAMIC_TASK_14"}}, "_meta": map[string]any{"origin": "actual-cli-config"}}
				default:
					result = map[string]any{}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
			}))
			defer peer.Close()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The original CLI init HEAD is distinct from model POSTs.
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}

				data, _ := io.ReadAll(r.Body)
				call := modelCalls.Add(1)
				if !bytes.Contains(data, []byte(`"name":"mcp__owned__calculate"`)) {
					t.Error("explicit CLI MCP never reached model")
				}
				hasStatic := bytes.Contains(data, []byte(`"name":"mcp__static__calculate"`))
				if hasStatic != (mode == "normal") {
					t.Error("strict/bare static discovery changed", mode, hasStatic)
				}
				if call%2 == 1 {
					response(w, "cli-mcp-start", "mcp__owned__calculate", map[string]any{"n": 7}, "")
				} else {
					if !bytes.Contains(data, []byte("MCP_DYNAMIC_TASK_14")) {
						t.Error("actual dynamic MCP result missing next inference")
					}
					response(w, "cli-mcp-done", "", nil, "CLI_MCP_TASK_FINISHED")
				}
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("private fixture unavailable keychain")
			}
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"type": "http", "url": "http://127.0.0.1:1/never"}, "static": map[string]any{"type": "http", "url": peer.URL + "/static"}}}), 0600)
			path := filepath.Join(o.WorkDir, "cli-mcp.json")
			_ = os.WriteFile(path, raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"type": "http", "url": "http://127.0.0.1:1/overridden"}}}), 0600)
			dynamic := string(raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"type": "http", "url": peer.URL + "/dynamic"}}}))
			for _, cold := range []bool{false, true} {
				flag := "--session-id"
				if cold {
					flag = "--resume"
				}
				argv := []string{"--print", "--tools", "", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Complete the actual CLI MCP task", flag, o.Arguments.SessionID, "--permission-mode", "bypassPermissions", "--mcp-config", path, dynamic}
				if mode == "strict" {
					argv = append(argv, "--strict-mcp-config")
				}
				if mode == "bare" {
					argv = append(argv, "--bare")
				}
				argv = append(argv, "--", "Use the dynamic MCP calculate tool")
				command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				code, err := RunCommand(context.Background(), command, o, strings.NewReader(""), &output, nil)
				if err != nil || code != 0 || !strings.Contains(output.String(), "CLI_MCP_TASK_FINISHED") {
					t.Fatal(cold, code, err, output.String())
				}
			}
			if dynamicCalls.Load() != 2 || modelCalls.Load() != 4 {
				t.Fatal("dynamic/cold task repeated or skipped", dynamicCalls.Load(), modelCalls.Load())
			}
			if mode == "normal" && staticLists.Load() != 2 || mode != "normal" && staticLists.Load() != 0 {
				t.Fatal("static discovery wrong", staticLists.Load())
			}
			files, _ := filepath.Glob(filepath.Join(config, "projects", "*", o.Arguments.SessionID+".jsonl"))
			if len(files) != 1 {
				t.Fatal("actual transcript absent", files)
			}
			transcript, _ := os.ReadFile(files[0])
			var found bool
			for _, line := range bytes.Split(transcript, []byte("\n")) {
				var record map[string]any
				if json.Unmarshal(line, &record) == nil && bytes.Contains(line, []byte("actual-cli-config")) {
					found = true
				}
			}
			if !found {
				t.Fatal("CLI MCP metadata not persisted for cold history")
			}
		})
	}
}
