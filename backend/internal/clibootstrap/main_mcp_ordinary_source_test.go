package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativeMCPOrdinaryFullCatalogueAgainstActualSourceServer(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("ToolSearch-%v", enabled), func(t *testing.T) { ordinaryCatalogueSource(t, enabled) })
	}
}
func ordinaryCatalogueSource(t *testing.T, enabled bool) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "fixture-not-a-secret", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
	if enabled {
		env["ENABLE_TOOL_SEARCH"] = "true"
	}
	fixture, err := filepath.Abs("../../testdata/native-mcp-ordinary.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	source.Dir = home
	for k, v := range env {
		source.Env = append(source.Env, k+"="+v)
	}
	source.Env = append(source.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE=1", "CC_HAHA_MCP_SECURE_READ_RECEIPT=1")
	source.Stdin = bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n")
	var stderr bytes.Buffer
	source.Stderr = &stderr
	output, err := source.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var response struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatal(err)
	}
	count := 20
	if enabled {
		count = 21
	}
	if len(response.Result.Tools) != count {
		t.Fatalf("actual default proxy Source catalogue has %d tools", len(response.Result.Tools))
	}
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	goSecureReads := 0
	n.RunSecurity = func(_ context.Context, command securestorage.Command) ([]byte, error) {
		if len(command.Args) == 0 || command.Args[0] != "find-generic-password" || command.Input != "" {
			t.Error("non-readonly external store primitive attempted")
		}
		goSecureReads++
		return nil, os.ErrNotExist
	}
	command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	host, err := NewNativeMCPToolContext(command, "darwin", false, false)
	if err != nil {
		t.Fatal(err)
	}
	checkOrdinarySecureReadReceipt(t, stderr.String(), goSecureReads)
	defer host.Close(context.Background())
	actual, err := host.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var goTools []map[string]any
	if err := json.Unmarshal(rawMain(actual), &goTools); err != nil {
		t.Fatal(err)
	}
	if len(goTools) != len(response.Result.Tools) {
		t.Fatalf("normal catalogue counts Go=%d Source=%d", len(goTools), len(response.Result.Tools))
	}
	for i, want := range response.Result.Tools {
		if !reflect.DeepEqual(goTools[i], want) {
			for key, value := range want {
				if !reflect.DeepEqual(goTools[i][key], value) {
					a, _ := json.Marshal(goTools[i][key])
					b, _ := json.Marshal(value)
					t.Errorf("tool %s field %s differs Go=%.450s Source=%.450s", want["name"], key, a, b)
				}
			}
			for key := range goTools[i] {
				if _, ok := want[key]; !ok {
					t.Errorf("tool %s extra field %s", want["name"], key)
				}
			}
			if d := os.Getenv("CC_HAHA_MCP_DIAGNOSTIC_DIR"); d != "" {
				_ = os.WriteFile(filepath.Join(d, "S650-catalog-Go.json"), rawMain(goTools), 0600)
				_ = os.WriteFile(filepath.Join(d, "S650-catalog-Source.json"), output, 0600)
			}
		}
	}
}
func mapKeysOrdinary(v map[string]any) []string {
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	return keys
}
