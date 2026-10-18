package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTerminalHelpV2OriginalDoublePressFooterAndCompletionKeys(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned whole HelpV2 Source required")
	}
	type keyStep struct {
		Input       string `json:"input"`
		DelayBefore int    `json:"delayBefore,omitempty"`
	}
	profiles := [][]keyStep{
		{{Input: "\x03"}, {Input: "\x03"}},
		{{Input: "\x04"}, {Input: "\x04"}},
		{{Input: "\x03"}, {Input: "\x04"}, {Input: "\x03"}},
		{{Input: "\x03"}, {Input: "\x03", DelayBefore: 900}, {Input: "\x03"}},
	}
	rows := []map[string]any{}
	closed := [][]bool{}
	for _, steps := range profiles {
		s := NewState(nil, 100, 80)
		data, _ := json.Marshal(TerminalHelpData{Version: "999.0.0-local", Shortcuts: TerminalShortcutHelp{Platform: "macos"}})
		s.openLocalPanel(&LocalPanel{ID: "owned-help", Kind: "help", HelpDialog: true, Presentation: data})
		paint := [][]string{s.LocalPanel.terminalHelpLines(100, 80, "dark", s.bindings)}
		isClosed := []bool{false}
		now := time.Now()
		for _, step := range steps {
			now = now.Add(time.Duration(step.DelayBefore+20) * time.Millisecond)
			key := "ctrl+c"
			if step.Input == "\x04" {
				key = "ctrl+d"
			}
			actions := s.HandleKey(Key{Name: key}, now)
			count := 0
			for _, action := range actions {
				if action.kind == actionLocalPanel && action.panel.Action == "help-close" {
					count++
				}
			}
			if count > 1 || s.Closing {
				t.Fatal("Help exit callback became whole application exit", actions)
			}
			isClosed = append(isClosed, s.LocalPanel == nil)
			if s.LocalPanel == nil {
				paint = append(paint, []string{})
			} else {
				paint = append(paint, s.LocalPanel.terminalHelpLines(100, 80, "dark", s.bindings))
			}
		}
		closed = append(closed, isClosed)
		rows = append(rows, map[string]any{"width": 100, "height": 80, "theme": "dark", "commands": []any{}, "keys": steps, "nativeRows": paint})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-v2.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELP_V2_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("whole original HelpV2 keys", err, string(output))
	}
	var source []struct {
		Snapshots []struct {
			Frame  string
			Cells  json.RawMessage
			Closed []struct {
				Text    string
				Options struct{ Display string }
			}
		}
		NativeCells []json.RawMessage
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, profile := range source {
		for j, frame := range profile.Snapshots {
			actualClosed := len(frame.Closed) > 0
			if actualClosed != closed[i][j] {
				t.Errorf("profile%d key%d actual Help onDone Source%t Go%t", i, j, actualClosed, closed[i][j])
			}
			if actualClosed {
				if len(frame.Closed) != 1 || frame.Closed[0].Text != "Help dialog dismissed" || frame.Closed[0].Options.Display != "system" {
					t.Error("actual original onDone", frame.Closed)
				}
			} else if !reflect.DeepEqual(frame.Cells, profile.NativeCells[j]) {
				t.Errorf("profile%d key%d whole original pending footer differs Source%q Go%q", i, j, frame.Frame, rows[i]["nativeRows"].([][]string)[j])
			}
		}
	}
}

func TestTerminalHelpDoublePressTimerRetiresWithMountedIDAndExpiresWithoutClosing(t *testing.T) {
	s := NewState(nil, 100, 80)
	data, _ := json.Marshal(TerminalHelpData{Version: "owned", Shortcuts: TerminalShortcutHelp{Platform: "macos"}})
	open := func(id string) {
		s.openLocalPanel(&LocalPanel{ID: id, Kind: "help", HelpDialog: true, Presentation: data})
	}
	now := time.Now()
	open("first")
	if actions := s.HandleKey(Key{Name: "ctrl+c"}, now); len(actions) != 0 || s.LocalPanel.helpExitPending != "Ctrl-C" {
		t.Fatal("initial mounted Help confirmation", actions)
	}
	// An actual same-ID projection refresh retains the component's hook state.
	open("first")
	if s.LocalPanel.helpExitPending != "Ctrl-C" {
		t.Fatal("same mounted Help lost its confirmation")
	}
	s.Tick(now.Add(doublePressWindow))
	if s.LocalPanel == nil || s.LocalPanel.helpExitPending != "" || len(s.LocalPanel.helpExitTimes) != 0 || s.Closing {
		t.Fatal("Help timeout did not clear just the pending footer")
	}
	s.HandleKey(Key{Name: "ctrl+c"}, now.Add(time.Second))
	s.HandleKey(Key{Name: "esc"}, now.Add(time.Second+20*time.Millisecond))
	open("second")
	if actions := s.HandleKey(Key{Name: "ctrl+c"}, now.Add(time.Second+40*time.Millisecond)); len(actions) != 0 || s.LocalPanel == nil || s.Closing {
		t.Fatal("retired component timer closed a fresh Help", actions)
	}
}
