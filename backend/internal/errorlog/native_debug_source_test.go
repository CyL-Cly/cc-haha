package errorlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeDebugLevelsCategoriesRawArgumentsAndFlushAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	logs := []struct {
		Message string `json:"message"`
		Level   string `json:"level"`
	}{
		{"plain debug", "debug"}, {"[API] private\nline", "info"}, {"hooks: private", "warn"}, {"Error: private", "error"}, {"api: noisy", "verbose"}, {`MCP server "private-peer": connected`, "debug"}, {"[ANT-ONLY] 1P event: private", "debug"}, {"AutoUpdaterWrapper: Installation type: development", "debug"},
	}
	profiles := []struct {
		args      []string
		env       map[string]string
		formatted bool
	}{
		{args: []string{"--debug"}}, {args: []string{"-d"}}, {args: []string{"-dapi"}}, {args: []string{"-d2e"}}, {args: []string{"--debug-to-stderr"}},
		{args: []string{"--debug-file=<FILE>"}}, {args: []string{"--debug-file", "<FILE>", "--debug-file=<OTHER>"}},
		{args: []string{"--debug-file=<FILE>", "--debug-file", "<OTHER>"}}, {args: []string{"--debug-file", "<FILE>", "-d2e"}},
		{args: []string{"--debug", "api"}}, {args: []string{"--debug=api,hooks"}}, {args: []string{"--debug=!1p,!error"}},
		{args: []string{"--debug=mcp"}}, {args: []string{"--debug=private-peer"}}, {args: []string{"--debug=installation"}},
		{args: []string{"--debug=api,!error"}}, {args: []string{"--debug=", "--debug=api"}}, {args: []string{"--debug=!"}},
		{args: []string{"--debug"}, env: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": "verbose"}},
		{args: []string{"--debug"}, env: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": " INFO "}},
		{args: []string{"--debug"}, env: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": "warn"}},
		{args: []string{"--debug"}, env: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": "error"}},
		{args: []string{"--debug"}, env: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": "unknown"}},
		{args: []string{"--debug"}, env: map[string]string{"NODE_ENV": "test"}},
		{args: []string{"-d2e"}, env: map[string]string{"NODE_ENV": "test"}, formatted: true},
		{env: map[string]string{"USER_TYPE": "ant"}}, {env: map[string]string{"DEBUG_SDK": "true"}, formatted: true}, {},
	}
	for i, p := range profiles {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, ".claude")
			if e := os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			env := map[string]string{"CLAUDE_CONFIG_DIR": config}
			for k, v := range p.env {
				env[k] = v
			}
			replacer := strings.NewReplacer("<FILE>", filepath.Join(config, "custom", "debug.log"), "<OTHER>", filepath.Join(config, "custom", "other.log"))
			args := append([]string{}, p.args...)
			for i, a := range args {
				args[i] = replacer.Replace(a)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-debug-sink.fixture.ts")
			input, _ := json.Marshal(map[string]any{"environment": env, "arguments": args, "cwd": root, "formatted": p.formatted, "logs": logs})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_DEBUG_LOG_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			var source struct {
				Before, After map[string]string
				Stderr        string
			}
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e, string(out))
			}
			if e = os.RemoveAll(config); e != nil {
				t.Fatal(e)
			}
			if e = os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			var terminal strings.Builder
			sink, e := NewNativeSink(NativeSinkOptions{Files: physicalSinkFiles{}, Metadata: func() Metadata {
				return Metadata{Environment: env, CWD: root, Home: root, ConfigDir: config, Platform: "darwin", SessionID: "private-error-session"}
			}, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Arguments: args, Formatted: p.formatted, Stderr: func(s string) { terminal.WriteString(s) }})
			if e != nil {
				t.Fatal(e)
			}
			defer sink.Close()
			for _, row := range logs {
				if e = sink.Debug(row.Message, row.Level); e != nil {
					t.Fatal(e)
				}
			}
			before := sinkSnapshot(t, config)
			sink.Flush()
			sink.Close()
			after := sinkSnapshot(t, config)
			if !reflect.DeepEqual(before, source.Before) || !reflect.DeepEqual(after, source.After) || terminal.String() != source.Stderr {
				t.Fatalf("profile%d beforeGo%v Source%v afterGo%v Source%v stderrGo%q Source%q", i, before, source.Before, after, source.After, terminal.String(), source.Stderr)
			}
		})
	}
}
