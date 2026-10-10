package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func waitActualLateBadge(t *testing.T, root string) {
	t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		if exists(filepath.Join(root, "ready")) {
			return
		}
		if body, err := os.ReadFile(filepath.Join(root, "broken")); err == nil {
			t.Fatalf("actual external badge failed before late READY: %s", body)
		}
		select {
		case <-timer.C:
			t.Fatal("physical badge never emitted late READY")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestActualWindowsBadgeLongStdoutLateREADYAndTurnCloseMatchesSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source needs pinned Bun")
	}
	src := physicalWindowsOptions(t)
	src.Environment["CC_HAHA_LATE_BADGE_ROOT"] = src.HomeDir
	script, _ := filepath.Abs("testdata/windows_late_badge.py")
	fixture, _ := filepath.Abs("testdata/windows_late_badge.fixture.ts")
	repo, _ := filepath.Abs("../../..")
	input, _ := json.Marshal(map[string]any{"repository": repo, "python": os.Getenv("CC_HAHA_FIXTURE_PYTHON"), "script": script, "marker": src.HomeDir})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = src.WorkDir
	for k, v := range src.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source late badge %v %s", err, out)
	}
	var oracle struct{ Running, Ready bool }
	if err = json.Unmarshal(out, &oracle); err != nil || !oracle.Running || !oracle.Ready {
		t.Fatalf("Source late badge result %s %v", out, err)
	}
	body, _ := os.ReadFile(filepath.Join(src.HomeDir, "pid"))
	pid, _ := strconv.Atoi(string(body))
	windowsPIDStopped(t, src, pid)
	o := physicalWindowsOptions(t)
	o.Environment["CC_HAHA_LATE_BADGE_ROOT"] = o.HomeDir
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.ConfigDir, ".runtime/win_cursor_badge.py"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s := fixtureService(t, o)
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Call(context.Background(), "late-badge-session", "get_app_state", json.RawMessage(`{"pid":42}`))
	waitActualLateBadge(t, o.HomeDir)
	body, _ = os.ReadFile(filepath.Join(o.HomeDir, "pid"))
	pid, _ = strconv.Atoi(string(body))
	if !processAlive(pid) {
		t.Fatal("Go badge exited after late READY")
	}
	if err = s.EndTurn(context.Background(), "late-badge-session"); err != nil {
		t.Fatal(err)
	}
	windowsPIDStopped(t, o, pid)
}
