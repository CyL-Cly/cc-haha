package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestPublicCLIClaudeAICatalogActualSecureFilesHTTPProxyAndSourceList(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	var mu sync.Mutex
	methods := []string{}
	authorizations := []string{}
	catalogs := 0
	physicalInput := ""
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if r.URL.Path != "/v1/mcp/owned-id" {
			t.Error("original proxy path lost", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request protocol.JSONObject
		if request.UnmarshalJSON(raw) != nil {
			t.Error("invalid JSONRPC")
			return
		}
		method := request.String("method")
		mu.Lock()
		methods = append(methods, method)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		if method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: json.RawMessage(`"2.0"`)}, protocol.JSONField{Name: "id", Value: request.Get("id")})
		if method == "initialize" {
			result.Set("result", json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"private-claudeai","version":"1"}}`))
		} else if method == "tools/call" {
			data, err := os.ReadFile(physicalInput)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			result.Set("result", encodeMCP(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(data)}}}))
		} else {
			result.Set("result", json.RawMessage(`{"tools":[{"name":"owned_read","description":"Read only the owned private test input","inputSchema":{"type":"object","properties":{}}}]} `))
		}
		_, _ = w.Write(encodeMCP(result))
	}))
	catalog := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/mcp_servers" || r.URL.RawQuery != "limit=1000" {
			t.Error("original catalog request lost", r.Method, r.URL)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fake-owned-token" || r.Header.Get("anthropic-beta") != "mcp-servers-2025-12-04" {
			t.Error("catalog actual credentials/headers absent")
		}
		mu.Lock()
		catalogs++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"owned-id","display_name":"Owned","url":"https://vendor.invalid/mcp"}]}`)
	})
	proxyURL, ca := claudeAICLIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/mcp_servers" {
			catalog.ServeHTTP(w, r)
		} else {
			proxy.Config.Handler.ServeHTTP(w, r)
		}
	}))
	proxy.Listener.Close()
	n, root := mcpCLIPrivateNative(t)
	n.Environment["HTTPS_PROXY"] = proxyURL
	n.Environment["NODE_EXTRA_CA_CERTS"] = ca
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	credentials := filepath.Join(config, ".credentials.json")
	payload := []byte(`{"claudeAiOauth":{"accessToken":"fake-owned-token","refreshToken":"fake-owned-refresh","expiresAt":9999999999999,"scopes":["user:inference","user:mcp_servers"],"subscriptionType":"pro"},"unknown_future":{"preserve":true}}`)
	if e := os.WriteFile(credentials, payload, 0600); e != nil {
		t.Fatal(e)
	}
	code, out, diagnostics, e := mcpCLIExecute(t, n, []string{"mcp", "list"})
	if e != nil || code != 0 || diagnostics != "" || !strings.Contains(out, "claude.ai Owned: https://vendor.invalid/mcp - ✓ Connected\n") {
		t.Fatal("actual public CLI list", code, e, out, diagnostics)
	}
	sourceHome := filepath.Join(root, "source-home")
	sourceConfig := filepath.Join(sourceHome, ".claude")
	if e = os.MkdirAll(sourceConfig, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(sourceConfig, ".credentials.json"), payload, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(sourceConfig, "settings.json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../testdata/mcp-cli-claudeai.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + sourceConfig, "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CLAUDEAI_CLI_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "HTTPS_PROXY=" + proxyURL, "NODE_EXTRA_CA_CERTS=" + ca, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	var sourceError bytes.Buffer
	command.Stderr = &sourceError
	sourceOut, e := command.Output()
	if e != nil {
		t.Fatal("actual Source CLI", e, sourceError.String())
	}
	if string(sourceOut) != out {
		t.Fatalf("actual Source stdout %q Go %q", sourceOut, out)
	}
	actual, e := os.ReadFile(credentials)
	if e != nil || !bytes.Equal(actual, payload) {
		t.Fatal("read-only list changed real private credentials", e)
	}
	physicalInput = filepath.Join(n.CWD, "private-mcp-input-P639.txt")
	if e = os.WriteFile(physicalInput, []byte("PHYSICAL_MCP_CLAUDEAI_ONLY_P639_8103\n"), 0600); e != nil {
		t.Fatal(e)
	}
	modelCalls := 0
	model := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		modelCalls++
		if modelCalls == 1 {
			var body protocol.JSONObject
			if body.UnmarshalJSON(data) != nil {
				t.Error("invalid actual model request")
				return
			}
			var tools []protocol.JSONObject
			_ = json.Unmarshal(body.Get("tools"), &tools)
			found := false
			for _, tool := range tools {
				if tool.String("name") == "mcp__claude_ai_Owned__owned_read" {
					found = true
				}
			}
			if !found {
				names := []string{}
				for _, tool := range tools {
					names = append(names, tool.String("name"))
				}
				mu.Lock()
				t.Log("actual MCP requests", catalogs, methods)
				mu.Unlock()
				t.Error("actual default Harness catalog omitted organization tool", names)
				jsonSchemaCLIResponse(w, "missing", "", nil, "missing organization tool")
				return
			}
			jsonSchemaCLIResponse(w, "owned-mcp-call", "mcp__claude_ai_Owned__owned_read", map[string]any{}, "")
		} else {
			if !bytes.Contains(data, []byte("PHYSICAL_MCP_CLAUDEAI_ONLY_P639_8103")) {
				t.Error("actual MCP physical result did not reach same Harness")
			}
			jsonSchemaCLIResponse(w, "owned-mcp-final", "", nil, "MCP_CLAUDEAI_HARNESS_P639_OK")
		}
	}))
	defer model.Close()
	n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	n.Environment["ANTHROPIC_AUTH_TOKEN"] = "fake-model-key"
	n.Environment["NO_PROXY"] = "127.0.0.1,localhost"
	n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	disabled := false
	var printDiagnostics bytes.Buffer
	print, e := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{Arguments: clibootstrap.PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 4, AllowedTools: []string{"mcp__claude_ai_Owned__owned_read"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: clibootstrap.DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &printDiagnostics})
	if e != nil {
		t.Fatal(e)
	}
	defer print.Close()
	if e = print.StartSession(ctx); e != nil {
		t.Fatal("actual native print startup", e)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"user"`)}, protocol.JSONField{Name: "session_id", Value: encodeMCP(print.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: encodeMCP(map[string]any{"role": "user", "content": "Use the organization owned_read tool to read the private test input."})})
	if e = print.RunTurn(ctx, user); e != nil {
		t.Fatal("actual native Harness", e)
	}
	if modelCalls != 2 {
		t.Log(printDiagnostics.String())
		t.Fatal("actual Harness tool transition", modelCalls)
	}
	if e = print.ShutdownSession(context.Background()); e != nil {
		t.Fatal("actual native shutdown", e)
	}
	mu.Lock()
	defer mu.Unlock()
	initializes := 0
	for _, method := range methods {
		if method == "initialize" {
			initializes++
		}
	}
	if initializes != 3 || catalogs != 3 {
		t.Fatal("both actual public callers not connected", catalogs, methods)
	}
	for _, authorization := range authorizations {
		if authorization != "Bearer fake-owned-token" {
			t.Fatal("proxy did not use same saved token")
		}
	}
}
