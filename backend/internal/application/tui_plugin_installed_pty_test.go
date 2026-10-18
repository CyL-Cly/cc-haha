package application

import (
	"context"
	"io"
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

	"github.com/charmbracelet/x/term"
)

func TestActualCLIPluginInstalledFlaggedSeenDismissStdioToolsAndSameHarnessPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY needs its own host")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-plugin-installed-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_installed_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_INSTALLED_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Installed plugin/MCP CLI %v %.14000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIPluginInstalledPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_INSTALLED_PTY_HELPER") != "1" {
		t.Skip("owned Installed PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-installed-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		switch n {
		case 1:
			if strings.Contains(string(obj(body).Get("tools")), "mcp__owned__calculate") {
				t.Error("disabled MCP leaked into next execution catalogue")
			}
			response(w, "installed-disabled", "", nil, "PUBLIC_INSTALLED_DISABLED_DONE_673")
		case 2:
			if !strings.Contains(string(body), "BULK_ACTUAL_ZETA") || !strings.Contains(string(obj(body).Get("tools")), "mcp__owned__calculate") {
				t.Error("actual installed Skill/MCP catalogue never reached same Harness", string(body))
			}
			response(w, "installed-tool", "mcp__owned__calculate", map[string]any{"n": 7}, "")
		case 3:
			if !strings.Contains(string(body), "PUBLIC_INSTALLED_ACTUAL_STDIO_RESULT") {
				t.Error("physical stdio result never reached model", string(body))
			}
			response(w, "installed-result", "", nil, "PUBLIC_INSTALLED_TOOL_DONE_673")
		case 4:
			response(w, "installed-human", "", nil, "PUBLIC_INSTALLED_NEXT_HUMAN_DONE_673")
		default:
			t.Error("local menu sent model request", n)
			response(w, "unexpected", "", nil, "UNEXPECTED")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
	// The original project scope preserves an ancestor warning alongside a
	// malformed nearest file. Both are real parser inputs, not peer failures.
	if err := os.WriteFile(filepath.Join(filepath.Dir(o.WorkDir), ".mcp.json"), []byte(`{"mcpServers":{"warning-peer":{"type":"http","url":"http://127.0.0.1:9/${CC_HAHA_WARNING_MISSING}"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, ".mcp.json"), []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	server := filepath.Join(o.WorkDir, "actual-owned-mcp.py")
	target := filepath.Join(o.WorkDir, "actual-owned-result.txt")
	pidfile := filepath.Join(o.WorkDir, "actual-owned-peer.pid")
	code := `import json,os,sys
with open(sys.argv[1],'w') as f:f.write(str(os.getpid()))
for line in sys.stdin:
 m=json.loads(line)
 if 'id' not in m:continue
 if m['method']=='initialize':r={'protocolVersion':'2025-11-25','capabilities':{'tools':{}},'serverInfo':{'name':'physical-owned','version':'1'}}
 elif m['method']=='tools/list':r={'tools':[{'name':'calculate','description':'Full actual MCP description without prompt truncation','inputSchema':{'type':'object','properties':{'n':{'type':'number','description':'Number to double'}},'required':['n']},'annotations':{'title':'Friendly calculator','readOnlyHint':True,'destructiveHint':True,'openWorldHint':True}}]}
 elif m['method']=='tools/call':
  with open(sys.argv[2],'w') as f:f.write(str(m['params']['arguments']['n']))
  r={'content':[{'type':'text','text':'PUBLIC_INSTALLED_ACTUAL_STDIO_RESULT'}]}
 else:r={}
 print(json.dumps({'jsonrpc':'2.0','id':m['id'],'result':r}),flush=True)
`
	if err := os.WriteFile(server, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}, "mcpServers": map[string]any{"owned": map[string]any{"type": "stdio", "command": "/usr/bin/python3", "args": []string{server, pidfile, target}}}}), 0600)
	_ = os.WriteFile(filepath.Join(config, "plugins", "flagged-plugins.json"), []byte(`{"plugins":{"flagged@mkt":{"flaggedAt":"2026-10-14T00:00:00.000Z"},"\ud800@mkt":{"flaggedAt":"2026-10-14T00:00:00.000Z"}}}`), 0600)
	_ = os.WriteFile(filepath.Join(home, "installed-config.txt"), []byte(config), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || status != 0 {
		t.Fatal(status, err)
	}
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(config, "plugins", "flagged-plugins.json"))
	flags := obj(obj(data).Get("plugins"))
	lone, _ := flags.GetPropertyJSON([]byte(`"\ud800@mkt"`))
	seen := obj(lone).String("seenAt") != "" && flags.Get("flagged@mkt") == nil
	result, _ := os.ReadFile(target)
	peerPID, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(peerPID))
	joined := pid > 0 && publicPTYProcessSignalZero(pid) != nil
	receipt := map[string]any{"requests": requests.Load(), "exitCode": status, "terminalRestored": reflect.DeepEqual(before, after), "actualFlagSeenAndDismissedRawIDPreserved": seen, "actualStdioToolResult": string(result) == "7", "actualPeerJoined": joined}
	_ = os.WriteFile(filepath.Join(home, "installed-receipt.json"), raw(receipt), 0600)
	for {
		if _, err = os.Stat(filepath.Join(home, "installed-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("slave termios proof never released helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 4 || !seen || string(result) != "7" || !joined || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)))
	}
}
