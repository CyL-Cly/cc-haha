package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualSourceOrdinaryMCPBashHintsFinalDataWhitespaceAndEarlyFailure(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if e := os.Mkdir(config, 0700); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}"), 0600)
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CLAUDE_CODE_TMPDIR": home, "PATH": "/usr/bin:/bin", "SHELL": "/bin/bash", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "owned-hints-fixture", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
	fixture, _ := filepath.Abs("../../testdata/native-mcp-ordinary.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE=1", "BUN_OPTIONS=--no-env-file")
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	n, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	main, e := NewMainCommand(MainCommandOptions{Arguments: []string{"mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	defer main.Close()
	host, e := NewNativeMCPToolContext(main, "darwin", false, false)
	if e != nil {
		t.Fatal(e)
	}
	defer host.Close(context.Background())
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 4096), 1<<20)
	for i, command := range []string{"printf ''", "printf '\\n\\n  leading  \\n\\n'", "printf 'tabs\\t\\t'", "printf 'inner\\t\\t\\nlast  \\n'", "printf '<claude-code-hint v=1 type=plugin value=x@agent-skills/>\\nvisible\\n'", "printf 'visible\\n<claude-code-hint v=2 type=plugin value=ignored/>\\n'", "printf 'inline <claude-code-hint v=1 type=plugin value=x@agent-skills/>'", "printf '<claude-code-hint v=1 type=plugin value=x@agent-skills/>\\nFAILURE\\n'; exit 7"} {
		args := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoteMain(command)})
		request := map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": map[string]any{"name": "Bash", "arguments": args}}
		data, _ := json.Marshal(request)
		if _, e = stdin.Write(append(data, '\n')); e != nil {
			t.Fatal(e)
		}
		if !reader.Scan() {
			t.Fatalf("Source row%d %v %s", i, reader.Err(), stderr.String())
		}
		var response protocol.JSONObject
		if e = response.UnmarshalJSON(reader.Bytes()); e != nil {
			t.Fatal(e, string(reader.Bytes()))
		}
		got, e := host.Call(ctx, "Bash", rawMain(args))
		if e != nil {
			t.Fatal(e)
		}
		var want protocol.JSONObject
		_ = want.UnmarshalJSON(response.Get("result"))
		for _, key := range []string{"content", "isError"} {
			a, b := got.Get(key), want.Get(key)
			if len(a) == 0 && len(b) == 0 {
				continue
			}
			if len(a) == 0 || len(b) == 0 {
				t.Fatalf("MCP row%d missing key%s Go %s Source %s", i, key, rawMain(got), rawMain(want))
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
				t.Fatalf("MCP row%d key%s command%q\nGo %s\nSource %s", i, key, command, rawMain(got), rawMain(want))
			}
		}
	}
}
