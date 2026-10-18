package errorlog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMCPDiagnosticsUseActualSourceDedicatedSinkDespitePrivacyGate(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	for _, profile := range []struct {
		name string
		env  map[string]string
	}{
		{"external", map[string]string{}},
		{"privacy-disabled", map[string]string{"DISABLE_ERROR_REPORTING": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}},
		{"debug", map[string]string{"DEBUG": "true"}},
		{"debug-error-filter", map[string]string{"DEBUG": "true", "CLAUDE_CODE_DEBUG_LOG_LEVEL": "error"}},
		{"ant-test", map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test"}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(home, "private-cwd-汉😀")
			server := strings.Repeat("private:汉😀/peer-", 22)
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-error-sink.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_ERROR_SINK=1", "PATH=" + home}
			input, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "server": server, "environment": profile.env})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal("actual Source MCP sink", err, string(output), stderr.String())
			}
			var source struct {
				Files   map[string]string
				Memory  []Row
				Blocked []string
			}
			if err := json.Unmarshal(output, &source); err != nil || len(source.Memory) != 0 || len(source.Blocked) != 0 {
				t.Fatal(err, string(output))
			}
			if err := os.RemoveAll(config); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			environment := map[string]string{"CLAUDE_CONFIG_DIR": config}
			for name, value := range profile.env {
				environment[name] = value
			}
			sink, err := NewNativeSink(NativeSinkOptions{Files: physicalSinkFiles{}, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Metadata: func() Metadata {
				return Metadata{Environment: environment, CWD: cwd, Home: home, ConfigDir: config, Platform: runtime.GOOS, SessionID: "private-mcp-log-session"}
			}})
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range []error{sink.MCPDebug(server, "OAuth complete, reconnected with 1 tool(s)"), sink.MCPErrorMessage(server, "OAuth flow failed after tool-triggered start: private denied"), sink.MCPError(server, Error{Name: "MCPError", Message: "private original message", Stack: "MCPError: private original message\n    at private-owned-frame"})} {
				if err != nil {
					t.Fatal(err)
				}
			}
			sink.Close()
			actual := sinkSnapshot(t, config)
			if !reflect.DeepEqual(actual, source.Files) {
				t.Fatal("dedicated MCP file/path/JSONL/debug gate differs from Source", actual, source.Files)
			}
			t.Logf("actual Source profile %s files=%d memory=%d blocked=%d", profile.name, len(actual), len(source.Memory), len(source.Blocked))
		})
	}
}
