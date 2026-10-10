package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBashTimeoutAutoBackgroundPredicateAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	commands := []string{"", "sleep", " sleep ", "sleep 1", "echo before; sleep", "sleep; echo after", "sleep >file", "sleep 2 >file", "echo x", "cat <<'EOF'\ndata\nEOF", "\n\ufeffsleep\u2000", "echo ${bad substitution}"}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": home, "autoCommands": commands})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4500s", e, out)
	}
	var source struct {
		Auto         []bool
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Auto) != len(commands) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4500s", e, out)
	}
	for i, command := range commands {
		got, e := nativeBashAutoBackgroundAllowed(command, "darwin")
		if e != nil || got != source.Auto[i] {
			t.Errorf("command%q Go%v/%v Source%v", command, got, e, source.Auto[i])
		}
	}
}
