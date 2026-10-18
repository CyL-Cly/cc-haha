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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIMCPAuthenticationManualCallbackClearEscCancellationAndGlobalClosePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY requires its own host")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-mcp-auth-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_mcp_auth_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_MCP_AUTH_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual MCP auth CLI %v %.16000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIMCPAuthenticationPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_MCP_AUTH_PTY_HELPER") != "1" {
		t.Skip("owned OAuth PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-mcp-auth-pty")); err != nil {
		t.Fatal(err)
	}
	var models, tokens, revokes, tools, cancelled atomic.Int32
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": peer.URL + "/mcp", "authorization_servers": []string{peer.URL}, "scopes_supported": []string{"read", "offline_access"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": peer.URL, "authorization_endpoint": peer.URL + "/authorize", "token_endpoint": peer.URL + "/token", "registration_endpoint": peer.URL + "/register", "revocation_endpoint": peer.URL + "/revoke", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read", "offline_access"}})
		case "/register":
			var registration map[string]any
			_ = json.NewDecoder(r.Body).Decode(&registration)
			registration["client_id"] = "private-public-cli-client"
			_ = json.NewEncoder(w).Encode(registration)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "private-code" || r.Form.Get("code_verifier") == "" {
				t.Error("real manual callback lost PKCE exchange", r.Form)
			}
			if n := tokens.Add(1); n > 1 {
				_ = os.WriteFile(filepath.Join(home, "auth-token-held"), []byte("held"), 0600)
				<-r.Context().Done()
				cancelled.Add(1)
				_ = os.WriteFile(filepath.Join(home, "auth-token-cancelled"), []byte("joined"), 0600)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-cli-auth-access", "refresh_token": "private-cli-auth-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": "read offline_access"})
		case "/revoke":
			revokes.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/mcp":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("Authorization") != "Bearer private-cli-auth-access" {
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
			var result any
			switch message.String("method") {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "physical-auth", "version": "1"}}
			case "tools/list":
				result = map[string]any{"tools": []any{map[string]any{"name": "inspect", "description": "Authenticated actual tool", "inputSchema": map[string]any{"type": "object"}}}}
			case "tools/call":
				tools.Add(1)
				result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "PUBLIC_ACTUAL_AUTHENTICATED_TOOL_RESULT"}}}
			default:
				t.Error("undeclared peer method", message.String("method"))
				result = map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result})
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
		switch n {
		case 1:
			if !strings.Contains(string(obj(body).Get("tools")), "mcp__auth__inspect") {
				t.Error("auth catalogue missing from same Harness")
			}
			response(w, "auth-tool", "mcp__auth__inspect", map[string]any{}, "")
		case 2:
			if !strings.Contains(string(body), "PUBLIC_ACTUAL_AUTHENTICATED_TOOL_RESULT") {
				t.Error("physical auth tool result absent")
			}
			response(w, "auth-done", "", nil, "PUBLIC_MCP_AUTH_TOOL_DONE_724")
		case 3, 4:
			if strings.Contains(string(obj(body).Get("tools")), "mcp__auth__inspect") {
				t.Error("clear/cancel left callable authenticated tool")
			}
			marker := "PUBLIC_MCP_AUTH_CLEARED_DONE_724"
			if n == 4 {
				marker = "PUBLIC_MCP_AUTH_CANCELLED_DONE_724"
			}
			response(w, "auth-next", "", nil, marker)
		default:
			t.Error("local OAuth escaped to model", n)
			response(w, "unexpected", "", nil, "UNEXPECTED")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{"mcp__auth__inspect"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	browser := filepath.Join(home, "private-auth-browser")
	_ = os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n"), 0700)
	o.Environment["BROWSER"] = browser
	o.Environment["AUTH_BROWSER_RECEIPT"] = filepath.Join(home, "auth-browser-url")
	o.Native.Environment["BROWSER"] = browser
	o.Native.Environment["AUTH_BROWSER_RECEIPT"] = filepath.Join(home, "auth-browser-url")
	privateBin := filepath.Join(home, "private-bin")
	if err := os.MkdirAll(privateBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateBin, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$AUTH_CLIPBOARD_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	o.Environment["PATH"] = privateBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	o.Environment["AUTH_CLIPBOARD_RECEIPT"] = filepath.Join(home, "auth-clipboard-url")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(`{"futurePrivateAuth":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}, "mcpServers": map[string]any{"auth": map[string]any{"type": "http", "url": peer.URL + "/mcp", "oauth": map[string]any{"callbackPort": port}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "auth-port.json"), raw(port), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	terminalEnvironment := []string{"TERM=xterm-256color"}
	for key, value := range o.Environment {
		terminalEnvironment = append(terminalEnvironment, key+"="+value)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, terminalEnvironment)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	connection, dialErr := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if connection != nil {
		_ = connection.Close()
	}
	credentialData, _ := os.ReadFile(filepath.Join(config, ".credentials.json"))
	unknown := string(obj(obj(credentialData).Get("futurePrivateAuth")).Get("keep")) == "true"
	receipt := map[string]any{"exitCode": code, "requests": models.Load(), "tokens": tokens.Load(), "revokes": revokes.Load(), "tools": tools.Load(), "heldTokenCancelledJoined": cancelled.Load() == 1, "callbackListenerClosed": dialErr != nil, "futureCredentialPreserved": unknown, "terminalRestored": reflect.DeepEqual(before, after)}
	_ = os.WriteFile(filepath.Join(home, "auth-receipt.json"), raw(receipt), 0600)
	for {
		if _, err = os.Stat(filepath.Join(home, "auth-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("slave termios proof never released auth helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if models.Load() != 4 || tokens.Load() != 2 || revokes.Load() != 2 || tools.Load() != 1 || cancelled.Load() != 1 || !unknown || dialErr == nil || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)))
	}
}
