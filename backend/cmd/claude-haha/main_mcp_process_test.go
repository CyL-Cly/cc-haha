package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPublicMCPCLIActualPrivateStdioHandshakeAndImportPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows native terminal lane separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY driver unavailable")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	config := filepath.Join(home, ".claude")
	for _, p := range []string{home, work, tmp, config} {
		if err = os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	log, err := build.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(log))
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "TERM=xterm-256color"}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = work
		cmd.Env = env
		var out, diagnostics bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diagnostics
		code := 0
		capturePublicGoCoverage(t, cmd)
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return code, out.String(), diagnostics.String()
	}
	peerPath, journal := filepath.Join(root, "peer.py"), filepath.Join(root, "stdio-journal.txt")
	peer := `import json,os,select,signal,sys
journal=os.open(os.environ['PRIVATE_MCP_JOURNAL'],os.O_WRONLY|os.O_CREAT|os.O_APPEND,0o600)
initialized=False
stopping=False
wake_read,wake_write=os.pipe()
def record(value):
 os.write(journal,(value+'\n').encode())
def stop(signum,frame):
 global stopping
 # A signal is only a stop request, not a peer consumption acknowledgement.
 # Wake the normal protocol loop; do not interrupt its journal writes/reads
 # with nested I/O or SystemExit inside Python's signal handler.
 stopping=True;os.write(wake_write,b'x')
signal.signal(signal.SIGINT,stop)
buffer=b''
while True:
 # Source cleanup sends SIGINT immediately after its notification write.
 # Consume the already-sent notification before committing graceful close.
 # If it was never sent, original SIGTERM/SIGKILL escalation still fails this
 # peer's journal assertion; no notification is synthesized or acknowledged.
 if stopping and initialized:
  record('closed');break
 ready,_,_=select.select([sys.stdin.fileno(),wake_read],[],[])
 if wake_read in ready:os.read(wake_read,4096)
 if sys.stdin.fileno() not in ready:continue
 data=os.read(sys.stdin.fileno(),65536)
 if not data:break
 buffer+=data
 while b'\n' in buffer:
  line,buffer=buffer.split(b'\n',1)
  request=json.loads(line);method=request['method'];record(method)
  if method=='initialize':
   assert request['params']['clientInfo']['name']=='claude-code'
   print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':{'protocolVersion':'2025-11-25','capabilities':{'tools':{}},'serverInfo':{'name':'private-stdio','version':'1'}}}),flush=True)
  elif method=='notifications/initialized':initialized=True
  elif 'id' in request:print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':{}}),flush=True)
os.close(journal);os.close(wake_read);os.close(wake_write)
`
	if err = os.WriteFile(peerPath, []byte(peer), 0600); err != nil {
		t.Fatal(err)
	}
	peerConfig, _ := json.Marshal(map[string]any{"type": "stdio", "command": python, "args": []string{peerPath}, "env": map[string]string{"PRIVATE_MCP_JOURNAL": journal}})
	code, out, diagnostics := run("mcp", "add-json", "stdio-private", string(peerConfig), "-s", "user")
	if code != 0 || diagnostics != "" {
		t.Fatal(code, out, diagnostics)
	}
	for _, args := range [][]string{{"mcp", "get", "stdio-private"}, {"mcp", "list"}} {
		code, out, diagnostics = run(args...)
		if code != 0 || diagnostics != "" || !strings.Contains(out, "✓ Connected") {
			t.Fatal(code, out, diagnostics)
		}
	}
	data, err := os.ReadFile(journal)
	if err != nil || strings.Count(string(data), "initialize\n") != 2 || strings.Count(string(data), "notifications/initialized\n") != 2 || strings.Count(string(data), "closed\n") != 2 {
		t.Fatal("actual stdio handshake or graceful cleanup", err, string(data))
	}
	if want := strings.Repeat("initialize\nnotifications/initialized\nclosed\n", 2); string(data) != want {
		t.Fatal("actual stdio notification must precede peer shutdown", string(data))
	}
	code, out, diagnostics = run("mcp", "remove", "stdio-private")
	if code != 0 || diagnostics != "" {
		t.Fatal(code, out, diagnostics)
	}
	for _, name := range []string{"existing", "existing_1"} {
		code, out, diagnostics = run("mcp", "add", name, "/usr/bin/true", "-s", "user")
		if code != 0 || diagnostics != "" {
			t.Fatal(code, out, diagnostics)
		}
	}
	desktop := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	if err = os.MkdirAll(filepath.Dir(desktop), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(desktop, []byte(`{"mcpServers":{"existing":{"command":"/usr/bin/true","args":["imported"]},"fresh":{"command":"/usr/bin/true","args":[]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	sourceTTY, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-secret-tty.fixture.ts")
	driver := exec.CommandContext(ctx, python, "testdata/mcp_import_pty.py", binary, root, work, bun, sourceTTY)
	driver.Env = env
	capturePublicGoCoverage(t, driver)
	log, err = driver.CombinedOutput()
	if err != nil {
		t.Fatal("actual public MCP import PTY", err, string(log))
	}
	if !strings.Contains(string(log), `"terminalRestored": true`) {
		t.Fatal(string(log))
	}
	data, err = os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var global map[string]any
	if err = json.Unmarshal(data, &global); err != nil {
		t.Fatal(err)
	}
	servers := global["mcpServers"].(map[string]any)
	if servers["existing_2"] == nil || servers["fresh"] == nil || servers["existing"] == nil || servers["existing_1"] == nil {
		t.Fatal("real selection/suffix mutations", string(data))
	}
	if entries, err := os.ReadDir(filepath.Join(config, "projects")); err == nil && len(entries) != 0 || err != nil && !os.IsNotExist(err) {
		t.Fatal("MCP management started an ASK session", entries, err)
	}
}
