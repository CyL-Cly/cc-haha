package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ordinarySourceClient struct {
	cmd     *exec.Cmd
	in      *os.File
	scanner *bufio.Scanner
	stderr  bytes.Buffer
	id      int
}

func ordinaryFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	if p, e := filepath.EvalSymlinks(home); e == nil {
		home = p
	}
	config := filepath.Join(home, ".claude")
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CLAUDE_CODE_TMPDIR": home, "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "fixture-not-a-secret", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(home, "absent-gitconfig"), "GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"}
	return home, env
}
func startOrdinarySource(t *testing.T, home, cwd string, env map[string]string) *ordinarySourceClient {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	fixture, _ := filepath.Abs("../../testdata/native-mcp-ordinary.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	c := &ordinarySourceClient{}
	c.cmd = exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	c.cmd.Dir = home
	for k, v := range env {
		c.cmd.Env = append(c.cmd.Env, k+"="+v)
	}
	c.cmd.Env = append(c.cmd.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE=1", "CC_HAHA_MCP_ORDINARY_CWD="+cwd)
	in, e := c.cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	c.in = in.(*os.File)
	out, e := c.cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	c.scanner = bufio.NewScanner(out)
	c.scanner.Buffer(make([]byte, 4096), 5*1024*1024)
	c.cmd.Stderr = &c.stderr
	if e = c.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = c.in.Close()
		if c.cmd.ProcessState == nil {
			_ = c.cmd.Process.Kill()
			_ = c.cmd.Wait()
		}
	})
	return c
}
func (c *ordinarySourceClient) call(t *testing.T, name string, args any) protocol.JSONObject {
	t.Helper()
	c.id++
	req := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}
	if _, e := c.in.Write(append(rawMain(req), '\n')); e != nil {
		t.Fatal(e)
	}
	if !c.scanner.Scan() {
		t.Fatal("Source response missing", c.scanner.Err(), ordinaryFixtureStderr(c))
	}
	var response protocol.JSONObject
	if e := response.UnmarshalJSON(c.scanner.Bytes()); e != nil {
		t.Fatal(e)
	}
	result, ok := response.Object("result")
	if !ok {
		failure, _ := response.Object("error")
		t.Fatal("Source RPC failure", failure.String("message"))
	}
	return result
}
func (c *ordinarySourceClient) close(t *testing.T) {
	t.Helper()
	_ = c.in.Close()
	if e := c.cmd.Wait(); e != nil {
		t.Fatal("Source close", e, c.stderr.String())
	}
}

// Worktree Source starts a persistent branch watcher. The disposable driver
// explicitly terminates that process after all request effects are observed.
func (c *ordinarySourceClient) disposeWatcher(t *testing.T) {
	t.Helper()
	_ = c.in.Close()
	if e := c.cmd.Process.Signal(os.Interrupt); e != nil {
		t.Fatal(e)
	}
	_ = c.cmd.Wait()
}

func ordinaryGoContext(t *testing.T, home, cwd string, env map[string]string) (*NativeMCPToolContext, *MainCommand) {
	t.Helper()
	n, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(_ context.Context, command securestorage.Command) ([]byte, error) {
		if len(command.Args) == 0 || command.Args[0] != "find-generic-password" || command.Input != "" {
			t.Error("non-readonly external store primitive attempted")
			return nil, cooperative.ErrRestricted
		}
		if raw := env["CC_HAHA_MCP_FAKE_SECURE_JSON"]; raw != "" {
			return []byte(raw), nil
		}
		return nil, os.ErrNotExist
	}
	bindCommandPrefetchFixture(t, n)
	command, e := NewMainCommand(MainCommandOptions{Arguments: []string{"mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(command.Close)
	host, e := NewNativeMCPToolContext(command, "darwin", false, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host, command
}
func ordinaryData(t *testing.T, result protocol.JSONObject) protocol.JSONObject {
	t.Helper()
	var blocks []protocol.JSONObject
	if e := json.Unmarshal(result.Get("content"), &blocks); e != nil || len(blocks) != 1 {
		t.Fatal("bad actual MCP content", e)
	}
	var data protocol.JSONObject
	if e := data.UnmarshalJSON([]byte(blocks[0].String("text"))); e != nil {
		t.Fatal("bad actual MCP data", e, blocks[0].String("text"))
	}
	return data
}

func ordinaryFixtureStderr(c *ordinarySourceClient) string {
	if c.cmd.ProcessState == nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return c.stderr.String()
}

func checkOrdinaryLogicalSecureReadReceipt(t *testing.T, stderr string) {
	t.Helper()
	const prefix = "MCP_SECURE_READ_RECEIPT:"
	var sourceReads struct {
		Reads int `json:"reads"`
	}
	found := false
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, prefix) {
			if e := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &sourceReads); e != nil {
				t.Fatal(e)
			}
			found = true
		}
	}
	if !found || sourceReads.Reads != 1 {
		t.Fatalf("actual direct-server logical source storage receipt changed: Source=%d want=1 found=%v", sourceReads.Reads, found)
	}
	t.Logf("actual direct-server logical empty credential consumer: Source opaque storage reads=%d; physical startup primitives are compared against whole main/serve separately", sourceReads.Reads)
}
