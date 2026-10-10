package main

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
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func mcpCLIPrivateNative(t *testing.T) (*clibootstrap.NativeBoundary, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"home", "work", "managed", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "home", ".claude"), "TMPDIR": filepath.Join(root, "tmp"), "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: filepath.Join(root, "work"), Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private MCP fixture Keychain unavailable")
	}
	return n, root
}
func mcpCLIExecute(t *testing.T, n *clibootstrap.NativeBoundary, args []string) (int, string, string, error) {
	t.Helper()
	parsed := cliargs.Parse(args, cliargs.Config{Version: version})
	if parsed.Parsed == nil {
		t.Fatalf("actual CLI parse %+v", parsed)
	}
	var out, diagnostics bytes.Buffer
	code, err := runNativeMCPCommand(context.Background(), args, *parsed.Parsed, n, nil, &out, &diagnostics, "darwin")
	return code, out.String(), diagnostics.String(), err
}
func TestMCPCLIPrivateConfigurationScopesUnknownFieldsAndRemoveConflict(t *testing.T) {
	n, root := mcpCLIPrivateNative(t)
	for _, scope := range []string{"local", "user", "project"} {
		code, out, _, err := mcpCLIExecute(t, n, []string{"mcp", "add", "-s", scope, "fixture", "/usr/bin/true", "one", "two"})
		if code != 0 || err != nil || !strings.Contains(out, "Added stdio MCP server fixture with command: /usr/bin/true one two to "+scope+" config\n") {
			t.Fatal(scope, code, err, out)
		}
	}
	code, out, diagnostics, err := mcpCLIExecute(t, n, []string{"mcp", "remove", "fixture"})
	if code != 1 || err != nil || out != "" || !strings.Contains(diagnostics, "exists in multiple scopes") || !strings.Contains(diagnostics, "-s project") {
		t.Fatal(code, err, out, diagnostics)
	}
	code, out, _, err = mcpCLIExecute(t, n, []string{"mcp", "remove", "fixture", "-s", "project"})
	if code != 0 || err != nil || !strings.Contains(out, "Removed MCP server fixture from project config") {
		t.Fatal(code, err, out)
	}
	projectPath := filepath.Join(n.CWD, ".mcp.json")
	project, err := os.ReadFile(projectPath)
	if err != nil || strings.Contains(string(project), `"fixture"`) {
		t.Fatal("physical project removal", err, string(project))
	}
	globalPath := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json")
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	var global protocol.JSONObject
	_ = global.UnmarshalJSON(data)
	global.Set("unknown_future", json.RawMessage(`{"keep":"\\ud800"}`))
	if err := os.WriteFile(globalPath, encodeMCP(global), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _, err = mcpCLIExecute(t, n, []string{"mcp", "add-json", "another", `{"type":"stdio","command":"/usr/bin/true","args":[],"future":"strip"}`, "-s", "user"})
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	data, _ = os.ReadFile(globalPath)
	if !bytes.Contains(data, []byte("unknown_future")) || bytes.Contains(data, []byte(`"future":"strip"`)) {
		t.Fatal("shared native mutation lost unknown root or retained invalid config field", string(data))
	}
	code, out, _, err = mcpCLIExecute(t, n, []string{"mcp", "reset-project-choices"})
	if code != 0 || err != nil || !strings.Contains(out, "approvals and rejections have been reset") {
		t.Fatal(code, err, out)
	}
	data, _ = os.ReadFile(globalPath)
	if !bytes.Contains(data, []byte(`"enableAllProjectMcpServers": false`)) {
		t.Fatal("physical project choices not changed", string(data))
	}
	_ = root
}
func encodeMCP(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestMCPCLIActualPrivateHTTPHandshakeGetListAndOAuthSecretCleanup(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(405)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(raw)
		method := request.String("method")
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		if method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		value := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: json.RawMessage(`"2.0"`)}, protocol.JSONField{Name: "id", Value: request.Get("id")})
		switch method {
		case "initialize":
			value.Set("result", json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"private-actual","version":"1"}}`))
		case "tools/list":
			value.Set("result", json.RawMessage(`{"tools":[]}`))
		default:
			value.Set("result", json.RawMessage(`{}`))
		}
		_, _ = w.Write(encodeMCP(value))
	}))
	defer peer.Close()
	n, _ := mcpCLIPrivateNative(t)
	n.Environment["MCP_CLIENT_SECRET"] = "private-fake-secret"
	code, out, _, err := mcpCLIExecute(t, n, []string{"mcp", "add", "--transport", "http", "--client-id", "fixture-client", "--client-secret", "remote", peer.URL})
	if code != 0 || err != nil || strings.Contains(out, "private-fake-secret") {
		t.Fatal(code, err, out)
	}
	code, out, _, err = mcpCLIExecute(t, n, []string{"mcp", "get", "remote"})
	if code != 0 || err != nil || !strings.Contains(out, "✓ Connected") || !strings.Contains(out, "client_secret configured") {
		t.Fatal(code, err, out)
	}
	code, out, _, err = mcpCLIExecute(t, n, []string{"mcp", "list"})
	if code != 0 || err != nil || !strings.Contains(out, "(HTTP) - ✓ Connected") {
		t.Fatal(code, err, out)
	}
	code, out, _, err = mcpCLIExecute(t, n, []string{"mcp", "remove", "remote"})
	if code != 0 || err != nil {
		t.Fatal(code, err, out)
	}
	credentials, _ := os.ReadFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json"))
	if bytes.Contains(credentials, []byte("private-fake-secret")) {
		t.Fatal("removed OAuth secret persisted")
	}
	mu.Lock()
	defer mu.Unlock()
	count := 0
	for _, method := range methods {
		if method == "initialize" {
			count++
		}
	}
	if count != 2 {
		t.Fatal("actual handshake count", methods)
	}
}
