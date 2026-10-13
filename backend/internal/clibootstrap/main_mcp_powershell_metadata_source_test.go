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
)

func TestWindowsMCPPowerShellFullDeclarationMatchesActualSourceToolAndZodSchemas(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	for _, edition := range []string{"pwsh", "powershell"} {
		t.Run(edition, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			executable := filepath.Join(f.home, "bin", edition)
			if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, nil, 0600); err != nil {
				t.Fatal(err)
			}
			f.n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
			f.n.Environment["PATH"] = "/usr/bin:/bin"
			f.n.Environment["TMPDIR"] = f.n.Temp
			command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"--bare", "mcp", "serve"}, Native: f.n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
			if err != nil {
				t.Fatal(err)
			}
			defer command.Close()
			host, err := NewNativeMCPToolContext(command, "windows", false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			catalogue, err := host.Catalog(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var actual protocol.JSONObject
			for _, tool := range catalogue {
				if tool.String("name") == "PowerShell" {
					actual = tool
				}
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			n := f.n
			child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
			child.Dir = n.Home
			child.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "ANTHROPIC_API_KEY=offline-ps-mcp-schema", "CLAUDE_CODE_POWERSHELL_PATH=" + executable, "PATH=/usr/bin:/bin"}
			child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "cwd": n.CWD}))
			var stderr bytes.Buffer
			child.Stderr = &stderr
			output, err := child.Output()
			if err != nil {
				t.Fatal("actual Source tool declaration", err, stderr.String())
			}
			var source struct{ Schema, Metadata, OutputSchema, MCPInputSchema protocol.JSONObject }
			if err = json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			expected := source.Metadata.Clone()
			expected.Set("description", source.Schema.Get("description"))
			expected.Set("inputSchema", rawMain(source.MCPInputSchema))
			expected.Set("outputSchema", rawMain(source.OutputSchema))
			var left, right any
			_ = json.Unmarshal(rawMain(actual), &left)
			_ = json.Unmarshal(rawMain(expected), &right)
			if !reflect.DeepEqual(left, right) {
				t.Fatal("Source whole MCP declaration differs", string(rawMain(actual)), string(rawMain(expected)))
			}
		})
	}
}
