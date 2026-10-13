package application

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCLIMCPPermissionPromptApprovesDeniesInvalidAndCancelsActualHTTP(t *testing.T) {
	for _, profile := range []string{"allow", "deny", "invalid-block", "invalid-schema", "missing", "cancel", "allow-rule-no-prompt", "deny-prompt-rule"} {
		t.Run(profile, func(t *testing.T) {
			var modelCalls, promptCalls atomic.Int32
			started := make(chan struct{}, 1)
			ended := make(chan struct{}, 1)
			cancelled := make(chan struct{}, 1)
			var target string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				data, _ := io.ReadAll(r.Body)
				message := obj(data)
				if message.Get("id") == nil {
					if message.String("method") == "notifications/cancelled" {
						select {
						case cancelled <- struct{}{}:
						default:
						}
					}
					w.WriteHeader(202)
					return
				}
				var result any
				switch message.String("method") {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "private-approval", "version": "1"}}
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]any{"name": "approval", "description": "Approval channel hidden from model", "inputSchema": map[string]any{"type": "object"}}}}
				case "tools/call":
					promptCalls.Add(1)
					params := obj(message.Get("params"))
					arguments := obj(params.Get("arguments"))
					if params.String("name") != "approval" || arguments.String("tool_name") != "Write" || !bytes.Contains(arguments.Get("input"), []byte("MCP_APPROVED_TASK")) || arguments.Get("tool_use_id") == nil {
						t.Error("actual permission producer payload changed", string(data))
					}
					if profile == "cancel" {
						started <- struct{}{}
						<-r.Context().Done()
						ended <- struct{}{}
						return
					}
					text := `{"behavior":"allow","updatedInput":{}}`
					if profile == "deny" {
						text = `{"behavior":"deny","message":"PRIVATE_MCP_DENIED"}`
					}
					if profile == "invalid-schema" {
						text = `{"behavior":"allow"}`
					}
					if profile == "invalid-block" {
						result = map[string]any{"content": []any{map[string]any{"type": "image", "data": "invalid-first-block", "mimeType": "image/png"}}}
					} else {
						result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
					}
				default:
					result = map[string]any{}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
			}))
			defer peer.Close()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				if profile != "missing" && bytes.Contains(data, []byte(`"name":"mcp__private__approval"`)) {
					t.Error("permission channel leaked into model tools")
				}
				if modelCalls.Add(1) == 1 {
					response(w, "mcp-permission-write", "Write", map[string]any{"file_path": target, "content": "MCP_APPROVED_TASK"}, "")
				} else {
					response(w, "mcp-permission-finished", "", nil, "MCP_PERMISSION_TASK_FINISHED")
				}
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("private fixture unavailable keychain")
			}
			target = filepath.Join(o.WorkDir, "permission-target.txt")
			name := "mcp__private__approval"
			if profile == "missing" {
				name = "mcp__private__missing"
			}
			config := string(raw(map[string]any{"mcpServers": map[string]any{"private": map[string]any{"type": "http", "url": peer.URL}}}))
			argv := []string{"--print", "--model", "fixture-model", "--tools", "Write", "--permission-mode", "default", "--permission-prompt-tool", name, "--mcp-config", config}
			if profile == "allow-rule-no-prompt" {
				argv = append(argv, "--allowedTools", "Write")
			}
			if profile == "deny-prompt-rule" {
				argv = append(argv, "--disallowedTools", "mcp__private__approval")
			}
			argv = append(argv, "--", "Write the private test artifact")
			command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
			if err != nil {
				t.Fatal(err)
			}
			o.Arguments = command.Arguments
			session, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- session.Submit(ctx, tui.Input{Text: command.Prompt}, nil) }()
			if profile == "cancel" {
				select {
				case <-started:
					cancel()
				case err := <-done:
					t.Fatal("prompt did not start", err)
				case <-ctx.Done():
					t.Fatal("prompt not admitted")
				}
				select {
				case <-cancelled:
				case <-time.After(3 * time.Second):
					t.Fatal("actual MCP request did not receive Source JSON-RPC cancellation")
				}
			}
			select {
			case err = <-done:
			case <-time.After(12 * time.Second):
				t.Fatal("actual permission task did not settle")
			}
			if profile == "missing" {
				if err == nil || !strings.Contains(err.Error(), "(passed via --permission-prompt-tool) not found") {
					t.Fatal("missing prompt did not fail original configuration", err)
				}
			} else if profile != "cancel" && err != nil {
				t.Fatal(err)
			}
			if profile == "cancel" {
				if err := session.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				select {
				case <-ended:
				case <-time.After(3 * time.Second):
					t.Fatal("runtime close did not cancel actual HTTP transport")
				}
			}
			data, readErr := os.ReadFile(target)
			writes := profile == "allow" || profile == "allow-rule-no-prompt" || profile == "deny-prompt-rule"
			if writes {
				if readErr != nil || string(data) != "MCP_APPROVED_TASK" {
					t.Fatal("approved physical task did not complete", readErr, string(data))
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal("rejected/invalid/canceled permission wrote artifact", readErr)
			}
			expectedPrompt := int32(1)
			if profile == "missing" || profile == "allow-rule-no-prompt" {
				expectedPrompt = 0
			}
			if promptCalls.Load() != expectedPrompt {
				t.Fatal("unexpected prompt side effect count", promptCalls.Load(), expectedPrompt)
			}
			if profile == "cancel" || profile == "missing" {
				if modelCalls.Load() != 1 {
					t.Fatal("canceled/missing prompt replayed model", modelCalls.Load())
				}
			} else if modelCalls.Load() != 2 {
				t.Fatal("normal task did not use same Harness continuation", modelCalls.Load())
			}
		})
	}
}
