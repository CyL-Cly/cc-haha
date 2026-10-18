package application

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIClearMCPMemoReturnsToNextHumanModelPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; Windows ConPTY requires host")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	if e = os.WriteFile(filepath.Join(home, "owned-clear-mcp-pty"), []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_clear_mcp_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_CLEAR_MCP_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil || !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("actual public MCP clear", e, string(out))
	}
}

func TestNativeTerminalClearMCPPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CLEAR_MCP_PTY_HELPER") != "1" {
		t.Skip("owned isolated MCP Clear CLI helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "owned-clear-mcp-pty")); e != nil {
		t.Fatal(e)
	}
	var initialize, lists, prompts, models atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
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
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			initialize.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{},"prompts":{}},"serverInfo":{"name":"owned-pty-clear","version":"1"}}`)
		case "tools/list":
			lists.Add(1)
			result = []byte(`{"tools":[{"name":"inspect","inputSchema":{"type":"object","properties":{}}}]}`)
		case "prompts/list":
			prompts.Add(1)
			result = []byte(`{"prompts":[{"name":"review","description":"owned clear prompt"}]}`)
		default:
			t.Error("unexpected MCP method", message.String("method"))
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, message.Get("id"), result)
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		count := models.Add(1)
		current := fmt.Sprintf("PUBLIC_CLEAR_MCP_HUMAN_%d", count)
		if r.Method != http.MethodPost || r.Header.Get("x-api-key") != "fake-only-loopback" || !strings.Contains(string(body), current) || !strings.Contains(string(body), `"name":"mcp__owned-peer__inspect"`) {
			t.Error("public clear did not restore next actual model MCP catalog", string(body))
		}
		if count == 2 && strings.Contains(string(body), "PUBLIC_CLEAR_MCP_HUMAN_1") {
			t.Error("clear retained old ordinary user history", string(body))
		}
		response(w, fmt.Sprintf("clear-mcp-%d", count), "", nil, fmt.Sprintf("PUBLIC_CLEAR_MCP_COMPLETE_%d", count))
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	o.Interactive = true
	o.ForegroundShellProgress = true
	o.Arguments.MCPConfigs = []string{fmt.Sprintf(`{"mcpServers":{"owned-peer":{"type":"http","url":%q}}}`, peer.URL)}
	changed := o.OnCommandsChanged
	o.OnCommandsChanged = func(rows []sdkhost.ControlCommand) error {
		if changed != nil {
			if e := changed(rows); e != nil {
				return e
			}
		}
		for _, row := range rows {
			if row.Name == "owned-peer:review (MCP)" {
				fmt.Fprintln(os.Stdout, "OWNED_CLEAR_MCP_CATALOG_READY")
				break
			}
		}
		return nil
	}
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color")
	code, e := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if _, e = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); e != nil {
		t.Fatal(e)
	}
	after, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	if initialize.Load() != 1 || lists.Load() != 1 || prompts.Load() != 1 || models.Load() != 2 {
		t.Fatal("public clear replaced cached peer", initialize.Load(), lists.Load(), prompts.Load(), models.Load())
	}
	receipt, _ := json.Marshal(map[string]any{"code": code, "terminalRestored": reflect.DeepEqual(before, after), "initialize": initialize.Load(), "lists": lists.Load(), "prompts": prompts.Load(), "models": models.Load()})
	if e = os.WriteFile(filepath.Join(home, "clear-mcp-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Stat(filepath.Join(home, "clear-mcp-pty-release")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY observer failed to release")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
