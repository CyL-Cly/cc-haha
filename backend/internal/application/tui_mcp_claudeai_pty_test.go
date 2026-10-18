package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIClaudeAIConnectorBrowserEnterClearCancelNextHarnessAndTermiosPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY requires its own host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "disposable-proxy-auth-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/tui_mcp_claudeai_pty.py", binary, home)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_MCP_PROXY_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual connector public CLI", err, string(output))
	}
	t.Log(string(output))
}

func TestNativeCLIClaudeAIConnectorPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_MCP_PROXY_PTY_HELPER") != "1" {
		t.Skip("owned proxy PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-proxy-auth-pty")); err != nil {
		t.Fatal(err)
	}
	var catalogs, connects, tools, models atomic.Int32
	proxy, ca := claudeAIAPIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-public-connector-account" {
			t.Error("actual cached identity missing", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Host == "api.anthropic.com" && r.Method == "GET" && r.URL.Path == "/v1/mcp_servers" {
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"display_name":"Owned","id":"mcprs-public","url":"https://unrequested-connector.invalid/mcp"}]}`)
			return
		}
		if r.Host != "mcp-proxy.anthropic.com" || r.URL.Path != "/v1/mcp/mcprs-public" {
			t.Error("unowned official HTTP (including account OAuth)", r.Host, r.URL)
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if _, err := os.Stat(filepath.Join(home, "proxy-browser-grant")); err != nil {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		message := obj(body)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch message.String("method") {
		case "initialize":
			connects.Add(1)
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "owned-public-proxy", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "inspect", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			tools.Add(1)
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "PUBLIC_PROXY_PHYSICAL_TOOL_RESULT_813"}}}
		default:
			t.Error("unowned actual proxy method", message.String("method"))
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result})
	}))
	tool := mcpclient.ToolName("claude.ai Owned", "inspect")
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := models.Add(1)
		if r.Header.Get("Authorization") != "Bearer private-public-connector-account" || r.Header.Get("x-api-key") != "" {
			t.Error("ordinary model lost same cached identity", r.Header)
		}
		present := strings.Contains(string(obj(body).Get("tools")), tool)
		if present != (n <= 3) {
			t.Error("public connector next tool catalog", n, present)
		}
		switch n {
		case 1:
			response(w, "proxy-call", tool, map[string]any{}, "")
		case 2:
			if !strings.Contains(string(body), "PUBLIC_PROXY_PHYSICAL_TOOL_RESULT_813") {
				t.Error("proxy result not returned to same Harness")
			}
			response(w, "proxy-tool-done", "", nil, "PUBLIC_PROXY_TOOL_DONE_813")
		case 3:
			response(w, "proxy-cancelled-clear", "", nil, "PUBLIC_PROXY_CANCELLED_CLEAR_DONE_813")
		case 4:
			response(w, "proxy-cleared", "", nil, "PUBLIC_PROXY_CONFIRMED_CLEAR_DONE_813")
		default:
			t.Error("unexpected public model HTTP", n)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = []string{tool}
	o.Stderr = os.Stderr
	for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(environment, "ANTHROPIC_API_KEY")
		environment["USER_TYPE"] = "external"
		environment["HTTPS_PROXY"], environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
		environment["NO_PROXY"] = "127.0.0.1,localhost"
		environment["BROWSER"], environment["AUTH_BROWSER_RECEIPT"] = filepath.Join(home, "private-proxy-browser"), filepath.Join(home, "proxy-browser-url")
	}
	browser := []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n")
	if err := os.WriteFile(filepath.Join(home, "private-proxy-browser"), browser, 0700); err != nil {
		t.Fatal(err)
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(`{"futurePrivateConnector":{"keep":true},"claudeAiOauth":{"accessToken":"private-public-connector-account","scopes":["user:inference","user:mcp_servers"],"expiresAt":9999999999999}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]string{"organizationUuid": "private-public-org", "accountUuid": "private-account"}, "hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Second)
	defer cancel()
	var environment []string
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err := nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	retained := strings.Contains(string(stored), "private-public-connector-account") && string(obj(obj(stored).Get("futurePrivateConnector")).Get("keep")) == "true"
	receipt := map[string]any{"exitCode": code, "catalogs": catalogs.Load(), "connections": connects.Load(), "tools": tools.Load(), "models": models.Load(), "cachedAccountAndUnknownFieldsPreserved": retained, "terminalRestored": reflect.DeepEqual(before, after)}
	if err := os.WriteFile(filepath.Join(home, "proxy-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(home, "proxy-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if catalogs.Load() != 1 || connects.Load() != 1 || tools.Load() != 1 || models.Load() != 4 || !retained || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)))
	}
}
