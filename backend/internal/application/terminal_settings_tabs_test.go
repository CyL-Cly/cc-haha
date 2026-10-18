package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTerminalSettingsTabsUnmountConfigKeepsWritesReentersFreshSnapshotAndUsage(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(e tui.Event) error {
		if e.LocalPanel != nil {
			copy := *e.LocalPanel
			panel = &copy
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
		t.Fatal(err)
	}
	firstID := panel.ID
	if len(panel.SettingsTabs) != 3 || panel.SettingsHeaderFocused {
		t.Fatal(panel)
	}
	apply := func(action, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row}, emit); err != nil {
			t.Fatal(err)
		}
	}
	apply("next-config", "autoCompactEnabled")
	apply("settings-tab:status", "")
	if panel.ID == firstID || panel.Kind != "status" || !panel.SettingsHeaderFocused {
		t.Fatal(panel)
	}
	global, err := s.process.TerminalConfigState(context.Background())
	if err != nil || string(obj(global.Get("global")).Get("autoCompactEnabled")) != "false" {
		t.Fatal(global, err)
	}
	apply("settings-tab:config", "")
	apply("next-config", "autoCompactEnabled")
	apply("close", "")
	global, err = s.process.TerminalConfigState(context.Background())
	if err != nil || string(obj(global.Get("global")).Get("autoCompactEnabled")) != "false" {
		t.Fatal("reentered Config reverted changes from earlier mount", global, err)
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "status", "", emit); err != nil {
		t.Fatal(err)
	}
	apply("settings-tab:usage", "")
	if panel.Kind != "usage" || !panel.SettingsHeaderFocused {
		t.Fatal(panel)
	}
	// Join the actual owned non-subscriber consumer before inspecting its
	// detached cache. No account or network is invented to expose the tab.
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	state.mu.Lock()
	job := state.usagePanel.job
	state.mu.Unlock()
	if job != nil {
		<-job.done
	}
	state.mu.Lock()
	loaded := state.usagePanel.value.Clone()
	loading := state.usagePanel.loading
	state.mu.Unlock()
	if loading || string(loaded.Get("utilization")) != "{}" || string(loaded.Get("subscriptionType")) != "null" {
		t.Fatal(loaded, loading)
	}
	apply("settings-tab:config", "")
	apply("next-config", "autoCompactEnabled")
	apply("settings-dismiss", "")
	global, err = s.process.TerminalConfigState(context.Background())
	if err != nil || string(obj(global.Get("global")).Get("autoCompactEnabled")) != "true" {
		t.Fatal("header close reverted content writes", global, err)
	}
	if handled, err := s.InvokeTerminalCommand(context.Background(), "usage", "", emit); handled || err != nil {
		t.Fatal("normal non-subscriber command gate was bypassed", handled, err)
	}
}

func TestTerminalSettingsTabsActualOriginalFocusAndNavigationCallbacks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-settings-tabs.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_SETTINGS_TABS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "kind": "config", "steps": []string{"next", "focusHeader", "next", "previous", "down", "focusHeader", "previous"}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.5000s", err, output)
	}
	var source struct {
		Initial map[string]bool
		Trace   []struct {
			Kind                     string
			HeaderFocused, Prevented bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if source.Initial["config"] || !source.Initial["status"] || !source.Initial["usage"] || len(source.Trace) != 7 || source.Trace[0].Kind != "config" || source.Trace[2].Kind != "usage" || source.Trace[3].Kind != "config" || source.Trace[4].HeaderFocused || !source.Trace[4].Prevented || source.Trace[6].Kind != "status" {
		t.Fatal(string(output))
	}
}
