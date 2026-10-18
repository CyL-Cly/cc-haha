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
)

func TestPromptShortcutHelpWholeOriginalRendererPaletteWrapAndBindingConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			for mode := 0; mode < 4; mode++ {
				s := NewState(nil, width, 80)
				s.Preferences.Theme = theme
				s.ShortcutHelp.Platform = "darwin"
				s.ShortcutHelp.FastModeAvailable = mode != 3
				if mode == 1 {
					s.ShortcutHelp.Newline = "shift + ⏎ for newline"
				}
				if mode == 2 {
					s.ShortcutHelp.Newline = "\\⏎ for newline"
				}
				rows = append(rows, map[string]any{"width": width, "theme": theme, "installed": mode == 1, "backslash": mode == 2, "disabledFast": mode == 3, "nativeRows": s.shortcutHelpRows()})
			}
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-shortcut-help.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SHORTCUT_HELP_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var expected []struct {
		Original           string
		Cells, NativeCells []json.RawMessage
	}
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output))
	}
	for i, row := range expected {
		if !reflect.DeepEqual(row.Cells, row.NativeCells) {
			t.Errorf("Original shortcut help profile%d %+v\nSource%q\nGo%q\nSourcecells%s\nGocells%s", i, rows[i], row.Original, strings.Join(rows[i]["nativeRows"].([]string), "\n"), mustJSON(row.Cells), mustJSON(row.NativeCells))
		}
	}
}
