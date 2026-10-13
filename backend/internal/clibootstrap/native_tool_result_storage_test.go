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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualMainPrintLargeMCPResultPersistsFullFileAndPhysicalReaderReachesNextModel(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprint("readonly-", concurrent), func(t *testing.T) {
			content := strings.Repeat("actual-result-row\n", 4000) + "ACTUAL_MCP_FULL_TAIL_RS35\n"
			var calls, rounds atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				var request protocol.JSONObject
				_ = request.UnmarshalJSON(data)
				if request.Get("id") == nil {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				var result json.RawMessage
				switch request.String("method") {
				case "initialize":
					result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-large-result","version":"1"}}`)
				case "tools/list":
					result = rawMain(map[string]any{"tools": []any{map[string]any{"name": "large", "description": "Large actual private MCP result", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}, "annotations": map[string]any{"readOnlyHint": concurrent}}}})
				case "tools/call":
					calls.Add(1)
					result = rawMain(map[string]any{"content": []any{map[string]any{"type": "text", "text": content}}})
				default:
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: request.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
			}))
			defer peer.Close()
			var persisted string
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				round := rounds.Add(1)
				if round == 1 {
					emitNativeTaskTool(w, "large-mcp-rs35", "mcp__fixture__large", map[string]any{})
					return
				}
				var request struct{ Messages []protocol.JSONObject }
				if err := json.Unmarshal(body, &request); err != nil {
					t.Error(err)
				}
				for _, message := range request.Messages {
					for _, block := range taskBlocks(message.Get("content")) {
						if block.String("type") != "tool_result" {
							continue
						}
						if string(block.Get("is_error")) == "true" {
							t.Errorf("large result became a tool error: %.400s", rawMain(block))
						}
						if block.String("tool_use_id") == "large-mcp-rs35" {
							text := block.String("content")
							_, rest, ok := strings.Cut(text, "Full output saved to: ")
							if !ok {
								t.Errorf("large actual result has no full-file reference: %.300s", text)
							} else {
								persisted = strings.Split(rest, "\n")[0]
							}
							if strings.Contains(text, "ACTUAL_MCP_FULL_TAIL_RS35") {
								t.Error("model preview includes hidden full tail")
							}
						}
					}
				}
				if round == 2 {
					script := "import json; print(json.load(open(" + string(rawMain(persisted)) + "))[0][\"text\"].splitlines()[-1])"
					emitNativeTaskTool(w, "read-mcp-rs35", "Bash", map[string]any{"command": "python3 -c " + "'" + strings.ReplaceAll(script, "'", "'\"'\"'") + "'", "description": "Read actual complete persisted JSON output"})
					return
				}
				if !strings.Contains(string(body), "ACTUAL_MCP_FULL_TAIL_RS35") {
					t.Error("physical JSON reader tail did not reach third model request")
				}
				emitPlanFixture(w, int(round), "", "ACTUAL_MCP_REFERENCE_USED_RS35")
			}))
			defer model.Close()
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-large-result-fake-key"
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})))
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Model: "fixture-model", MaxTurns: 5, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			defer func() {
				if err := m.ShutdownSession(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if err = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Use the actual MCP output, read its persisted JSON file tail with Python, and report."})})); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || rounds.Load() != 3 || persisted == "" {
				t.Fatal("real result consumer did not close", calls.Load(), rounds.Load(), persisted)
			}
			data, err := os.ReadFile(persisted)
			if err != nil {
				t.Fatal(err)
			}
			var parts []protocol.JSONObject
			if err = json.Unmarshal(data, &parts); err != nil || len(parts) != 1 || parts[0].String("text") != content {
				t.Fatal("complete physical MCP output changed", len(data), err)
			}
			if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				dir, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
				if e == nil && filepath.Dir(persisted) != dir {
					t.Errorf("foreign result directory %s/%s", persisted, dir)
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
