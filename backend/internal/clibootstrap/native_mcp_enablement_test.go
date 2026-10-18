package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeMCPExecutionEnablementAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, defaultDirectory := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "default"}[defaultDirectory], func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.n.CWD, f.n.ConfigDirExplicit = f.home, !defaultDirectory
			if defaultDirectory {
				// Match the Source launch's absent property. Changing a legacy
				// annotation alone cannot remove the actual environment value.
				delete(f.n.Environment, "CLAUDE_CONFIG_DIR")
			}
			if err := os.MkdirAll(f.n.Temp, 0700); err != nil {
				t.Fatal(err)
			}
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Close(context.Background()) })
			path := filepath.Join(f.config, ".claude.json")
			if defaultDirectory {
				path = filepath.Join(f.home, ".claude.json")
			}
			if p.global.Path() != path {
				t.Fatalf("global path=%s want=%s", p.global.Path(), path)
			}
			project := func(value string) string {
				key, _ := json.Marshal(f.home)
				return `{"projects":{` + string(key) + `:` + value + `}}`
			}
			files := []*string{nil}
			for _, value := range []string{`{}`, `{`, `null`, `[]`, `{"projects":null}`, `{"projects":[]}`, project(`null`), project(`{"disabledMcpServers":null}`), project(`{"disabledMcpServers":"owned"}`), project(`{"disabledMcpServers":["owned",false]}`), project(`{"disabledMcpServers":["other"]}`), project(`{"disabledMcpServers":["owned"]}`), project(`{"disabledMcpServers":[]}`), "\ufeff" + project(`{"disabledMcpServers":["owned"]}`), `{`} {
				value := value
				files = append(files, &value)
			}
			steps := []map[string]any{}
			observations := []map[string]any{}
			for i, file := range files {
				name := "owned"
				if i == len(files)-1 {
					name = "quoted\" 中文"
				}
				if file == nil {
					err = os.Remove(path)
					if os.IsNotExist(err) {
						err = nil
					}
				} else {
					err = os.WriteFile(path, []byte(*file), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				steps = append(steps, map[string]any{"file": file, "name": name})
				enabled, err := p.MCPExecutionEnabled(context.Background(), name, protocol.NewJSONObject())
				if err != nil {
					observations = append(observations, map[string]any{"error": err.Error()})
				} else {
					observations = append(observations, map[string]any{"enabled": enabled})
				}
			}
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-enablement.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_ENABLEMENT_FIXTURE=1", "CLAUDE_CODE_SKIP_REMOTE_PREFETCH=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"defaultDirectory": defaultDirectory, "steps": steps})
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			expected, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source MCP enablement: %v %s", err, stderr.String())
			}
			actual, _ := json.Marshal(map[string]any{"path": path, "observations": observations})
			var a, b any
			if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &b) != nil {
				t.Fatal("invalid observations", string(actual), string(expected))
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("Go=%s Source=%s", actual, expected)
			}
		})
	}
}
