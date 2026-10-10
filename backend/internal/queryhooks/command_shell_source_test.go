package queryhooks

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

func TestCommandHookShellPrefixAndPowerShellDiscoveryMatchActualSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("original POSIX PowerShell discovery")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	run := func(input map[string]any, environment ...map[string]string) []byte {
		t.Helper()
		input["homeDir"] = home
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
		if len(environment) > 0 {
			for k, v := range environment[0] {
				for i, entry := range cmd.Env {
					if strings.HasPrefix(entry, k+"=") {
						cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
						break
					}
				}
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
		cmd.Stdin = bytes.NewReader(commandJSON(input))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e, stderr.String())
		}
		return out
	}
	profiles := []map[string]string{}
	for _, prefix := range []string{"/bin/sh -c", "/path with spaces/wrapper -x -c", "wrapper", "C:\\Program Files\\wrapper.exe -c", "quote's wrapper -c", "", "weird-$`!;name"} {
		for _, command := range []string{"echo exact", "echo 'quoted $`! text'", "printf \"semi;space\"", "line1\nline2", ""} {
			profiles = append(profiles, map[string]string{"prefix": prefix, "command": command})
		}
	}
	var source struct{ Rows []string }
	if e := json.Unmarshal(run(map[string]any{"shells": profiles}), &source); e != nil || len(source.Rows) != len(profiles) {
		t.Fatal(e)
	}
	for i, p := range profiles {
		actual, e := FormatHookShellPrefix(p["prefix"], p["command"])
		if e != nil || actual != source.Rows[i] {
			t.Errorf("prefix%d Go%q %v Source%q", i, actual, e, source.Rows[i])
		}
	}
	first, second, empty := filepath.Join(home, "first"), filepath.Join(home, "second"), filepath.Join(home, "empty")
	for _, dir := range []string{first, second, empty} {
		_ = os.Mkdir(dir, 0700)
	}
	for _, path := range []string{filepath.Join(first, "pwsh"), filepath.Join(first, "powershell"), filepath.Join(second, "powershell")} {
		if e := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	cases := []map[string]any{{"path": first, "override": nil}, {"path": second, "override": nil}, {"path": empty, "override": nil}, {"path": first, "override": "not-powershell"}, {"path": first, "override": filepath.Join(second, "powershell")}, {"path": first, "override": "pwsh"}, {"path": first, "override": "  powershell  "}, {"path": first, "override": filepath.Join(empty, "pwsh")}, {"path": empty, "override": filepath.Join(first, "pwsh")}}
	for i, p := range cases {
		env := map[string]string{"PATH": p["path"].(string)}
		if value, ok := p["override"].(string); ok {
			env["CLAUDE_CODE_POWERSHELL_PATH"] = value
		}
		var original struct{ Rows []*string }
		if e := json.Unmarshal(run(map[string]any{"powershell": []any{map[string]any{}}}, env), &original); e != nil || len(original.Rows) != 1 {
			t.Fatal(e)
		}
		resolver := NewCommandShellResolver(env, runtime.GOOS)
		got := resolver.PowerShell()
		want := ""
		if original.Rows[0] != nil {
			want = *original.Rows[0]
		}
		if got != want {
			t.Errorf("discovery%d Go%q Source%q", i, got, want)
		}
	}
	resolver := NewCommandShellResolver(map[string]string{"PATH": first}, runtime.GOOS)
	before := resolver.PowerShell()
	_ = os.Remove(filepath.Join(first, "pwsh"))
	if !reflect.DeepEqual(before, resolver.PowerShell()) {
		t.Fatal("PowerShell detection did not retain original cached promise outcome")
	}
}
