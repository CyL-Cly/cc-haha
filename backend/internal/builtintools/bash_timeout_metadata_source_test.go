package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBashTimeoutEnvironmentAndSilentMetadataAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	rows := []map[string]string{{}, {"BASH_DEFAULT_TIMEOUT_MS": "200junk"}, {"BASH_DEFAULT_TIMEOUT_MS": "  +12.3 "}, {"BASH_DEFAULT_TIMEOUT_MS": "-10"}, {"BASH_DEFAULT_TIMEOUT_MS": "0"}, {"BASH_DEFAULT_TIMEOUT_MS": "0x20"}, {"BASH_DEFAULT_TIMEOUT_MS": "\ufeff900000tail", "BASH_MAX_TIMEOUT_MS": "12"}, {"BASH_DEFAULT_TIMEOUT_MS": "Infinity"}, {"BASH_MAX_TIMEOUT_MS": "+1000000ms"}, {"BASH_DEFAULT_TIMEOUT_MS": "2e3"}, {"BASH_DEFAULT_TIMEOUT_MS": "9007199254740993"}, {"BASH_MAX_TIMEOUT_MS": "999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999"}}
	commands := []string{"", "mkdir x", "mv a b && cp b c", "touch x || echo fallback", "touch x | cat", "touch x > file", "true", "false || mkdir x", "export A=B; unset A", "mkdir x >&2", "wait", "echo x && touch x", "\ufeffmkdir\u2000x", "touch \"x y\"", "touch x; :", "touch x || printf x", "touch x > f; chmod +x x", "echo ${bad substitution}", "touch x 2>&1", "touch x >f && touch y"}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": home, "timeoutRows": rows, "silentCommands": commands})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3500s", e, out)
	}
	var source struct {
		TimeoutRows    []struct{ DefaultMs, MaxMs *float64 }
		SilentCommands []bool
		Capabilities   struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.TimeoutRows) != len(rows) || len(source.SilentCommands) != len(commands) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.3500s", e, out)
	}
	for i, row := range rows {
		for j, v := range []float64{nativeBashDefaultTimeout(row), nativeBashMaxTimeout(row)} {
			want := []*float64{source.TimeoutRows[i].DefaultMs, source.TimeoutRows[i].MaxMs}[j]
			if want == nil {
				if !math.IsInf(v, 1) {
					t.Errorf("overflow Go=%v", v)
				}
			} else if v != *want {
				t.Errorf("env%v Go%v Source%v", row, v, *want)
			}
		}
	}
	for i, command := range commands {
		if got := nativeBashSilent(command, "darwin"); got != source.SilentCommands[i] {
			t.Errorf("command%q Go%v Source%v", command, got, source.SilentCommands[i])
		}
	}
	// Actual timer primitive mapping, including values admitted by the Source
	// numeric schema; the advertised maximum does not change execution input.
	for _, row := range []struct {
		input string
		want  float64
		delay time.Duration
	}{{`{}`, 120000, 120000 * time.Millisecond}, {`{"timeout":0}`, 120000, 120000 * time.Millisecond}, {`{"timeout":3.9}`, 3.9, 3 * time.Millisecond}, {`{"timeout":-1}`, -1, time.Millisecond}, {`{"timeout":900000}`, 900000, 900000 * time.Millisecond}, {`{"timeout":2147483648}`, 2147483648, time.Millisecond}} {
		value := input(row.input)
		got := nativeBashTimeout(value, nil)
		if got != row.want || nativeBashTimerDuration(got) != row.delay {
			t.Errorf("input%s timeout%v timer%v", row.input, got, nativeBashTimerDuration(got))
		}
	}
}
