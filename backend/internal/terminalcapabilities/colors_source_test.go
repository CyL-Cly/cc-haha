package terminalcapabilities_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/terminalcapabilities"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceColorLibrariesThemeProducerTmuxAndChalkFraming(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	type profile struct {
		Env      map[string]string `json:"env"`
		Platform string            `json:"platform"`
		Release  string            `json:"release"`
		TTY      bool              `json:"tty"`
		Args     []string          `json:"args"`
		Theme    string            `json:"theme"`
		Text     string            `json:"text"`
		Roles    []string          `json:"roles"`
	}
	environments := []map[string]string{{}, {"NO_COLOR": "1", "TERM": "xterm-256color"}, {"FORCE_COLOR": ""}, {"FORCE_COLOR": "false"}, {"FORCE_COLOR": "true"}, {"FORCE_COLOR": "0"}, {"FORCE_COLOR": "2"}, {"FORCE_COLOR": "3"}, {"FORCE_COLOR": "-1"}, {"FORCE_COLOR": "bad"}, {"FORCE_COLOR": "\ufeff2tail"}, {"FORCE_COLOR": "\u00852"}, {"TERM": "dumb"}, {"TERM": "xterm-256color"}, {"TERM": "xterm-kitty"}, {"TERM": "xterm-ghostty"}, {"TERM": "wezterm"}, {"TERM": "xterm"}, {"TERM": "ANSI"}, {"COLORTERM": "truecolor"}, {"COLORTERM": ""}, {"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.1"}, {"TERM_PROGRAM": "Apple_Terminal"}, {"TERM_PROGRAM": "vscode", "TERM": "xterm-256color"}, {"TERM_PROGRAM": "vscode", "TERM": "xterm-256color", "TMUX": "owned"}, {"COLORTERM": "truecolor", "TMUX": "owned", "CLAUDE_CODE_TMUX_TRUECOLOR": "1"}, {"CI": ""}, {"CI": "1", "GITHUB_ACTIONS": ""}, {"CI": "1", "TRAVIS": ""}, {"CI": "1", "CI_NAME": "codeship"}, {"TF_BUILD": "", "AGENT_NAME": ""}, {"TEAMCITY_VERSION": "9.0.0"}, {"TEAMCITY_VERSION": "9.01.0"}, {"TEAMCITY_VERSION": "10.0"}}
	rows := []profile{}
	for _, environment := range environments {
		for _, platform := range []string{"darwin", "linux", "win32"} {
			for _, tty := range []bool{false, true} {
				for _, args := range [][]string{{}, {"--no-color"}, {"--color=256"}, {"--color=truecolor"}, {"--", "--color=truecolor"}} {
					rows = append(rows, profile{Env: environment, Platform: platform, Release: "10.0.14393", TTY: tty, Args: args, Theme: "dark", Text: "text", Roles: []string{"success", "warning", "error"}})
				}
			}
		}
	}
	for _, release := range []string{"6.1.7601", "10.0.10585", "10.0.10586", "10.0.14930", "10.0.14931", "11.0.20000", "10.0.invalid"} {
		rows = append(rows, profile{Env: map[string]string{}, Platform: "win32", Release: release, TTY: true, Args: []string{}, Theme: "dark", Text: "text", Roles: []string{"success"}})
	}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, level := range []string{"0", "1", "2", "3"} {
			for _, text := range []string{"", "plain", "中文😀", "A\nB", "A\r\nB\nC", "A\x1b[39mB\x1b[22mC", "\nA\n"} {
				rows = append(rows, profile{Env: map[string]string{"FORCE_COLOR": level}, Platform: "darwin", Release: "", TTY: true, Args: []string{}, Theme: theme, Text: text, Roles: []string{"success", "warning", "error"}})
			}
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-setup.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "colorRows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_SETUP_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual color Source %v %.5000s", err, output)
	}
	type observedSupport struct {
		Level *float64
	}
	var source struct {
		Values []struct {
			ChalkSupport, HyperlinkSupport json.RawMessage
			InkLevel                       *float64
			Roles                          []string
			Dim                            string
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Values) != len(rows) {
		t.Fatalf("actual color Source transport %v %.5000s", err, output)
	}
	for i, row := range rows {
		got := source.Values[i]
		for modern, raw := range map[bool]json.RawMessage{false: got.ChalkSupport, true: got.HyperlinkSupport} {
			actual := terminalcapabilities.ColorLibrary(row.Env, row.Platform, row.Release, row.TTY, row.Args, modern)
			expected := terminalcapabilities.ColorSupport{}
			if string(raw) != "false" {
				var value observedSupport
				if err = json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				expected.Supported = true
				expected.Level = math.NaN()
				if value.Level != nil {
					expected.Level = *value.Level
				}
			}
			if actual.Supported != expected.Supported || actual.Level != expected.Level && !(math.IsNaN(actual.Level) && math.IsNaN(expected.Level)) {
				t.Errorf("row%d modern%v library actual%+v Source%+v profile%+v", i, modern, actual, expected, row)
			}
		}
		level := terminalcapabilities.InkColorLevel(row.Env, terminalcapabilities.ColorLibrary(row.Env, row.Platform, row.Release, row.TTY, row.Args, false).Level)
		expectedLevel := math.NaN()
		if got.InkLevel != nil {
			expectedLevel = *got.InkLevel
		}
		if level != expectedLevel && !(math.IsNaN(level) && math.IsNaN(expectedLevel)) {
			t.Errorf("row%d Ink level%v Source%v", i, level, expectedLevel)
		}
		for index, role := range row.Roles {
			value := tui.TerminalThemeRole(row.Theme, role)
			open := tui.TerminalThemeRoleEscape(row.Theme, role, false)
			var red, green, blue int
			if _, err := fmt.Sscanf(value, "rgb(%d,%d,%d)", &red, &green, &blue); err == nil {
				open = terminalcapabilities.RGBColorEscape(red, green, blue, level)
			}
			actual := terminalcapabilities.WrapColor(row.Text, open, "\x1b[39m", level)
			if actual != got.Roles[index] {
				t.Errorf("row%d role%s profile%+v actual%q Source%q", i, role, row, actual, got.Roles[index])
			}
		}
		if actual := terminalcapabilities.WrapColor(row.Text, "\x1b[2m", "\x1b[22m", level); actual != got.Dim {
			t.Errorf("row%d dim actual%q Source%q", i, actual, got.Dim)
		}
	}
	t.Logf("actual original color library/ink/chalk profiles=%d", len(rows))
}
