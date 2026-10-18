//go:build !windows

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestActualSourceGlobalTeamsCleanupBudgetRealForceExitReapsHeldPane(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, dir := range []string{".claude", "tmp", "bin"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fifo := filepath.Join(home, "pane-release")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	defer releaseTerminalTeamsFIFO(fifo)
	terminalTeamsPrivatePanePeer(t, home)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams-shutdown.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SHUTDOWN_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_SIMPLE=1", "PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin", "USER_TYPE=external", "TMUX=" + filepath.Join(home, "private-socket") + ",1,0", "CC_HAHA_TEAM_PANE_RECEIPT=" + filepath.Join(home, "pane-requests.jsonl"), "CC_HAHA_TEAM_HELD_PANE=%1", "CC_HAHA_TEAM_PANE_RELEASE=" + fifo}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "team": "source-global-close", "file": map[string]any{"name": "source-global-close", "createdAt": 42, "members": []map[string]any{{"name": "team-lead", "agentId": "lead"}, {"name": "held", "agentId": "held", "tmuxPaneId": "%1", "backendType": "tmux"}}}}))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("whole Source shutdown", err, string(out))
	}
	var start struct {
		PID int
		At  int64
	}
	data, err := os.ReadFile(filepath.Join(home, "shutdown-start.json"))
	if err != nil || json.Unmarshal(data, &start) != nil {
		t.Fatal(err, string(data))
	}
	elapsed := time.Now().UnixMilli() - start.At
	if elapsed < 1950 || elapsed > 3000 {
		t.Fatal("original whole cleanup budget/actual exit", elapsed, string(out))
	}
	data, err = os.ReadFile(filepath.Join(home, "pane-requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatal("original orphan cleanup native process", string(data))
	}
	var pane struct {
		PID     int
		Command string
		Args    []string
	}
	if json.Unmarshal([]byte(lines[0]), &pane) != nil || pane.Command != "tmux" || len(pane.Args) != 3 || pane.Args[0] != "kill-pane" || pane.Args[2] != "%1" {
		t.Fatal(string(data))
	}
	terminalTeamsUntil(t, func() bool { return syscall.Kill(pane.PID, 0) == syscall.ESRCH }, "original real forceExit left held pane primitive alive")
	if _, err := os.Stat(filepath.Join(home, "pane-release")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "teams", "source-global-close", "config.json")); err != nil {
		t.Fatal("Source global budget deleted config after held native cleanup", err)
	}
	t.Logf("Original full gracefulShutdown→runCleanupFunctions→cleanupSessionTeams→actual held pane: exit0 in%dms, held native PID%d ESRCH, team config retained; no exit mock, no released FIFO", elapsed, pane.PID)
}
