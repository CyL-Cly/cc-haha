package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTerminalIdleQueueLocalUIAndQueryGateMatchActualSourceHook(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]bool{}
	for _, query := range []bool{false, true} {
		for _, ui := range []bool{false, true} {
			rows = append(rows, map[string]bool{"queryActive": query, "localUI": ui})
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-idle-queue.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_IDLE_QUEUE_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source idle queue %v %.4000s", err, output)
	}
	var source struct {
		Rows []struct {
			Processed bool
			Remaining int
		}
		Blocked []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		s := &Session{}
		s.SetTerminalLocalUIActive(row["localUI"])
		if row["queryActive"] {
			s.turnMu.Lock()
		}
		s.mu.Lock()
		canStart := s.canStartTerminalIdleInputLocked()
		s.mu.Unlock()
		processed := false
		if canStart && s.turnMu.TryLock() {
			processed = true
			s.turnMu.Unlock()
		}
		if row["queryActive"] {
			s.turnMu.Unlock()
		}
		if processed != source.Rows[i].Processed || (source.Rows[i].Remaining == 0) != processed {
			t.Errorf("query=%v UI=%v Go=%v Source=%+v", row["queryActive"], row["localUI"], processed, source.Rows[i])
		}
	}
}
