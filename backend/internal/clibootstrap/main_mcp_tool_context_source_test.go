package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativeMCPBareFullCatalogAgainstActualSourceServer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", ".claude.json"} {
		if err = os.WriteFile(filepath.Join(config, name), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_SIMPLE": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ENABLE_TOOL_SEARCH": "false", "USER_TYPE": "external", "NODE_ENV": "production"}
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		t.Error("bare serve attempted actual secure read")
		return nil, os.ErrNotExist
	}
	command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"--bare", "mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	host, err := NewNativeMCPToolContext(command, "darwin", false, false)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := host.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-serve.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	source.Dir = home
	for k, v := range env {
		source.Env = append(source.Env, k+"="+v)
	}
	source.Env = append(source.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_CLI_MCP_SERVE_SOURCE_FIXTURE=1")
	source.Stdin = bytes.NewReader([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n"))
	var stderr bytes.Buffer
	source.Stderr = &stderr
	output, err := source.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var response protocol.JSONObject
	if err = response.UnmarshalJSON(bytes.TrimSpace(output)); err != nil {
		t.Fatal(err, string(output))
	}
	result, _ := response.Object("result")
	got := rawMain(actual)
	want := result.Get("tools")
	var x, y any
	_ = json.Unmarshal(got, &x)
	_ = json.Unmarshal(want, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("bare MCP complete serialized catalogue mismatch\nGo%s\nSource%s", got, want)
	}
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
