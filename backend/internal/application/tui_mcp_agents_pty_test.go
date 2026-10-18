package application

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIAgentOnlyMCPPreauthenticationCancelChildToolAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix public PTY; ConPTY needs own host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-agent-mcp-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_mcp_agents_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_AGENT_MCP_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual agent MCP public CLI %v %.16000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIAgentOnlyMCPPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_AGENT_MCP_PTY_HELPER") != "1" {
		t.Skip("owned agent MCP PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-agent-mcp-pty")); err != nil {
		t.Fatal(err)
	}
	var models, tokens, inits, calls atomic.Int32
	target := filepath.Join(home, "actual-agent-mcp-tool.txt")
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": peer.URL + "/mcp", "authorization_servers": []string{peer.URL}, "scopes_supported": []string{"read"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": peer.URL, "authorization_endpoint": peer.URL + "/authorize", "token_endpoint": peer.URL + "/token", "registration_endpoint": peer.URL + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read"}})
		case "/register":
			var registration map[string]any
			_ = json.NewDecoder(r.Body).Decode(&registration)
			registration["client_id"] = "private-agent-client"
			_ = json.NewEncoder(w).Encode(registration)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "private-agent-code" || r.Form.Get("code_verifier") == "" {
				t.Error("agent browser callback lost PKCE", r.Form)
			}
			tokens.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-agent-access", "token_type": "Bearer", "expires_in": 3600, "scope": "read"})
		case "/mcp":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("Authorization") != "Bearer private-agent-access" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+peer.URL+`/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			body, _ := io.ReadAll(r.Body)
			message := obj(body)
			if message.Get("id") == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			result := raw(map[string]any{})
			switch message.String("method") {
			case "initialize":
				if models.Load() == 0 {
					t.Error("agent pre-authentication eagerly connected a global peer")
				}
				inits.Add(1)
				result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"private-agent-peer","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[{"name":"inspect","description":"Agent-only actual authenticated tool","inputSchema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}]}`)
			case "tools/call":
				value := obj(obj(message.Get("params")).Get("arguments")).String("value")
				if value != "actual child side effect" {
					t.Error("agent actual tool input", value)
				}
				if err := os.WriteFile(target, []byte(value), 0600); err != nil {
					t.Error(err)
				}
				calls.Add(1)
				result = []byte(`{"content":[{"type":"text","text":"PUBLIC_AGENT_MCP_ACTUAL_TOOL_RESULT"}]}`)
			default:
				t.Error("unexpected agent MCP request", message.String("method"))
			}
			_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: raw("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := models.Add(1)
		hasAgentTool := false
		for _, tool := range array(obj(body).Get("tools")) {
			if tool.String("name") == "mcp__agentauth__inspect" {
				hasAgentTool = true
			}
		}
		switch n {
		case 1:
			if hasAgentTool {
				t.Error("agent-only tool leaked into parent catalogue")
			}
			response(w, "agent-parent", "Agent", map[string]any{"subagent_type": "auth-worker", "description": "actual pre-authenticated agent", "prompt": "Call the actual agent-only MCP inspect with value actual child side effect and report completion."}, "")
		case 2:
			if !strings.Contains(string(body), "AGENT_AUTH_CHILD_SYSTEM") || !hasAgentTool {
				t.Error("real configured child lost its prompt or authenticated catalogue", string(body))
			}
			response(w, "agent-tool", "mcp__agentauth__inspect", map[string]any{"value": "actual child side effect"}, "")
		case 3:
			if !strings.Contains(string(body), "PUBLIC_AGENT_MCP_ACTUAL_TOOL_RESULT") {
				t.Error("physical tool result absent from child")
			}
			response(w, "agent-child-done", "", nil, "PUBLIC_AGENT_MCP_CHILD_DONE")
		case 4:
			if !strings.Contains(string(body), "PUBLIC_AGENT_MCP_CHILD_DONE") || hasAgentTool {
				t.Error("child result/parent isolated catalogue lost", string(body))
			}
			response(w, "agent-parent-done", "", nil, "PUBLIC_AGENT_MCP_PARENT_DONE_928")
		case 5:
			response(w, "agent-next-human", "", nil, "PUBLIC_AGENT_MCP_NEXT_HUMAN_DONE_928")
		default:
			t.Error("local MCP panel escaped to model", n)
			response(w, "unexpected", "", nil, "UNEXPECTED")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{"Agent", "mcp__agentauth__inspect"}
	o.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	o.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	browser := filepath.Join(home, "private-agent-browser")
	if err = os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$AGENT_BROWSER_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["BROWSER"] = browser
		env["AGENT_BROWSER_RECEIPT"] = filepath.Join(home, "agent-browser-url")
		env["MCP_OAUTH_CALLBACK_PORT"] = strconv.Itoa(port)
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	agentsDir := filepath.Join(config, "agents")
	if err = os.MkdirAll(agentsDir, 0700); err != nil {
		t.Fatal(err)
	}
	definition := "---\nname: auth-worker\ndescription: Actual agent-only MCP consumer\ntools: mcp__agentauth__inspect\nmodel: inherit\nmaxTurns: 4\nmcpServers: " + string(raw([]any{map[string]any{"agentauth": map[string]string{"type": "http", "url": peer.URL + "/mcp"}}})) + "\n---\nAGENT_AUTH_CHILD_SYSTEM\n"
	if err = os.WriteFile(filepath.Join(agentsDir, "auth-worker.md"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	environment := []string{"TERM=xterm-256color"}
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	connection, dialErr := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if connection != nil {
		_ = connection.Close()
	}
	data, _ := os.ReadFile(target)
	receipt := map[string]any{"requests": models.Load(), "tokens": tokens.Load(), "initializes": inits.Load(), "tools": calls.Load(), "exitCode": code, "actualChildToolSideEffect": string(data) == "actual child side effect", "callbackListenerClosed": dialErr != nil, "terminalRestored": reflect.DeepEqual(before, after)}
	if err = os.WriteFile(filepath.Join(home, "agent-mcp-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "agent-mcp-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("slave termios proof absent")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if models.Load() != 5 || tokens.Load() != 1 || inits.Load() != 1 || calls.Load() != 1 || string(data) != "actual child side effect" || dialErr == nil || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)))
	}
}
