package server

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

func TestDisconnectBackgroundAndRootBoundaryAgainstActualFrozenSource(t *testing.T) {
	rows := []map[string]any{
		{"shells": []bool{false}, "cli": true, "waiting": true},
		{"shells": []bool{false}, "cli": true, "waiting": false},
		{"shells": []bool{false}, "cli": false},
		{"shells": []bool{false}, "pending": true},
		{"shells": []bool{false}, "team": true},
		{"shells": []bool{false}, "agents": 1},
		{"shells": []bool{false}, "tools": 1},
		{"shells": []bool{false}, "computer": 1},
		{"shells": []bool{true}},
		{"shells": []bool{false}, "cli": true, "waiting": true, "events": []any{map[string]any{"type": "assistant", "parent_tool_use_id": "child"}}},
		{"shells": []bool{false}, "cli": true, "waiting": true, "events": []any{map[string]any{"type": "stream_event", "parent_tool_use_id": nil}}},
		{"shells": []bool{false}, "cli": true, "waiting": true, "events": []any{map[string]any{"type": "system", "subtype": "init"}, map[string]any{"type": "result"}}},
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-disconnect-policy.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_DISCONNECT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source disconnect %v %.3000s", err, output)
	}
	var expected []struct {
		Background, Waiting                  bool
		Clears                               int
		PermissionCeiling, BackgroundCeiling int64
		RuntimeExit                          struct{ Status, Summary string }
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		if expected[i].PermissionCeiling != disconnectedWorkCeiling.Milliseconds() || expected[i].BackgroundCeiling != disconnectedWorkCeiling.Milliseconds() {
			t.Fatal("Go deadline differs from actual Source", expected[i])
		}
		if expected[i].RuntimeExit.Status != "failed" || expected[i].RuntimeExit.Summary != "Background shell: runtime connection lost; task completion could not be confirmed" {
			t.Fatal("frozen MAIN runtime-loss policy changed", expected[i].RuntimeExit)
		}
		d := (&runtimeSession{}).disconnectState()
		d.cliRunning, _ = row["cli"].(bool)
		d.rootWaiting, _ = row["waiting"].(bool)
		for _, stopped := range row["shells"].([]bool) {
			if !stopped {
				d.tasks["shell"] = disconnectTask{}
			}
		}
		if row["agents"] == 1 {
			d.tasks["agent"] = disconnectTask{agent: true}
		}
		if row["tools"] == 1 {
			d.tool["tool"] = true
		}
		if row["computer"] == 1 {
			d.computer["computer"] = true
		}
		if events, ok := row["events"].([]any); ok {
			for _, event := range events {
				data, _ := json.Marshal(event)
				d.observeSDK(lifecycleMessage(string(data)))
			}
		}
		pending, _ := row["pending"].(bool)
		team, _ := row["team"].(bool)
		got := d.decision(pending, false, team, false) == "background"
		if got != expected[i].Background || d.rootWaiting != expected[i].Waiting {
			t.Errorf("row%d Go background%v/waiting%v Source%+v", i, got, d.rootWaiting, expected[i])
		}
	}
}
