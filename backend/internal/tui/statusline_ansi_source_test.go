package tui

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

func TestActualSourceStatusLineAnsiStylesControlsAndTruncateCells(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profiles := []map[string]any{}
	for _, row := range []struct {
		text           string
		width, padding int
	}{
		{"abcdefghijk", 8, 2}, {"你好world🙂", 7, 1}, {"A\nB", 20, 0},
		{"\x1b[31mRED\x1b[0m plain", 30, 0}, {"\x1b[1;3;4;7;9mSTYLE\x1b[0m", 30, 0},
		{"\x1b[38;2;1;2;3mRGB\x1b[48;5;42mBG\x1b[0m", 30, 0},
		{"A\x1b[2J\x1b[10;10H\x1b]52;c;evil\x07B\x1b]0;bad title\x07C", 30, 0},
		{"A\x1b[8mB\x1b[5mC\x1b[0mD", 30, 0},
		{"\x1b]8;;https://fixture.invalid/link\x1b\\LINK\x1b]8;;\x1b\\", 30, 0},
		{"A", 1, 0}, {"ABC", 2, 0}, {"你好", 2, 0}, {"A\nBCDEFG", 4, 0}, {"ABCD\nE", 4, 0},
		{"\x1b[31;1mA\nB\x1b[22;39mC", 20, 0},
		{"\x1b[1;2mBOTH\x1b[22mPLAIN", 30, 0},
		{"\x1b[38:2::7:8:9mRGB\x1b[48:5:42mBG\x1b[0m", 30, 0},
		{"\x1b[4:3;58;2;40;50;60mUNDER\x1b[24mOFF", 30, 0},
		{"\x1b[31mABCD\x1b[32mEFGH", 5, 0},
		{"\x1b]8;;https://fixture.invalid/link\x1b\\LONG-LINK\x1b]8;;\x1b\\ plain", 8, 0},
		{"A\tB\rC\bD\x1b(0E\x1bXsecret\x1b\\F", 30, 0},
		{"AB", 1, 0}, {"ABC", 0, 0}, {"ABC", 3, 4}, {"\x1b[38;5;300mINDEX\x1b[38;2;300;0;0mRGB", 30, 0},
	} {
		for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
			for _, links := range []bool{false, true} {
				profiles = append(profiles, map[string]any{"text": row.text, "width": row.width, "padding": row.padding, "theme": theme, "links": links, "nativeRows": TerminalStatusLineTextRowsForTheme(row.text, row.width, row.padding, theme, links)})
			}
		}
	}
	for _, padding := range []float64{0.25, 0.5, 1.25, 1.5, 1.75, -2, 20} {
		profiles = append(profiles, map[string]any{"text": "ABCDEFGHIJK", "width": 10, "padding": padding, "theme": "dark", "links": false, "nativeRows": TerminalStatusLineTextRowsForThemeNumberPadding("ABCDEFGHIJK", 10, padding, "dark", false)})
	}
	input, _ := json.Marshal(map[string]any{"home": home, "settings": map[string]any{}, "rendererProfiles": profiles})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "FORCE_COLOR=3"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual original Ansi renderer", err, string(output))
	}
	var original struct {
		Profiles []struct {
			Original, WrapPreview string
			Cells, NativeCells    []json.RawMessage
		}
		Blocked []string
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 || len(original.Profiles) != len(profiles) {
		t.Fatal(err, string(output))
	}
	for i, row := range original.Profiles {
		source, _ := json.Marshal(row.Cells)
		native, _ := json.Marshal(row.NativeCells)
		if string(source) != string(native) {
			t.Errorf("profile%d original Ansi text/style cell mismatch (wrap=%q)\nSourceANSI %q\nGoRows %#v\nSourceCells %s\nGoCells %s", i, row.WrapPreview, row.Original, profiles[i]["nativeRows"], source, native)
		}
	}
	t.Logf("actual original Ansi/staticRender profiles=%d, blocked=%d", len(original.Profiles), len(original.Blocked))
}
