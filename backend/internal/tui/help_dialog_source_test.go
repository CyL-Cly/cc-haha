package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalHelpV2WholeOriginalTabsAndPainter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			commands := []protocol.JSONObject{}
			for _, source := range []string{`{"name":"help","description":"first","builtin":true}`, `{"name":"sample","description":"custom (user)","builtin":false}`} {
				var row protocol.JSONObject
				_ = json.Unmarshal([]byte(source), &row)
				commands = append(commands, row)
			}
			data, _ := json.Marshal(TerminalHelpData{Version: "999.0.0-local", Commands: commands, Shortcuts: TerminalShortcutHelp{Platform: "macos"}})
			p := &localPanelState{props: LocalPanel{HelpDialog: true, Presentation: data}, headerFocused: true}
			paint := [][]string{}
			for _, tab := range []string{"general", "commands", "custom-commands"} {
				p.helpTab = tab
				paint = append(paint, p.terminalHelpLines(width, 80, theme, DefaultKeybindings("macos", false, KeybindingFeatures{})))
			}
			rows = append(rows, map[string]any{"width": width, "height": 80, "theme": theme, "commands": []map[string]any{{"name": "help", "type": "local-jsx", "description": "first"}, {"name": "help", "type": "local-jsx", "description": "second"}, {"name": "sample", "type": "prompt", "description": "custom", "source": "userSettings"}}, "keys": []string{"\t", "\t"}, "nativeRows": paint})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-v2.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELP_V2_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var result []struct {
		Snapshots []struct {
			Frame string
			Cells json.RawMessage
		}
		NativeCells []json.RawMessage
	}
	if json.Unmarshal(output, &result) != nil || len(result) != len(rows) {
		t.Fatal(string(output))
	}
	for i, row := range result {
		for j, frame := range row.Snapshots {
			if !reflect.DeepEqual(frame.Cells, row.NativeCells[j]) {
				t.Errorf("Whole original HelpV2 profile%d tab%d Source%q Go%q", i, j, frame.Frame, strings.Join(rows[i]["nativeRows"].([][]string)[j], "\n"))
			}
		}
	}
}
