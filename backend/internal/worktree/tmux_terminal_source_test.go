package worktree

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceTmuxITerm2DirectAndTerminalFallbackOverrides(t *testing.T) {
	rows := []map[string]string{{}, {"TERM_PROGRAM": "iTerm.app"}, {"ITERM_SESSION_ID": "session"}, {"TERM": "iTerm.app"}, {"TERM": "iTerm.app", "TERM_PROGRAM": "other"}, {"TERM": "iTerm.app", "__CFBundleIdentifier": "com.unknown"}, {"TERM": "iTerm.app", "SESSIONNAME": "session"}}
	for _, key := range []string{"CURSOR_TRACE_ID", "VisualStudioVersion", "TMUX", "STY", "KONSOLE_VERSION", "GNOME_TERMINAL_SERVICE", "XTERM_VERSION", "VTE_VERSION", "TERMINATOR_UUID", "KITTY_WINDOW_ID", "ALACRITTY_LOG", "TILIX_ID", "WT_SESSION", "MSYSTEM", "ConEmuANSI", "ConEmuPID", "ConEmuTask", "WSL_DISTRO_NAME", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		rows = append(rows, map[string]string{"TERM": "iTerm.app", key: "active"})
	}
	for _, pair := range [][2]string{{"VSCODE_GIT_ASKPASS_MAIN", "cursor"}, {"VSCODE_GIT_ASKPASS_MAIN", "windsurf"}, {"VSCODE_GIT_ASKPASS_MAIN", "antigravity"}, {"__CFBundleIdentifier", "com.vendor.goland"}, {"__CFBundleIdentifier", "com.vscodium"}, {"TERMINAL_EMULATOR", "JetBrains-JediTerm"}} {
		rows = append(rows, map[string]string{"TERM": "iTerm.app", pair[0]: pair[1]})
	}
	home := t.TempDir()
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	fixture, _ := filepath.Abs("testdata/tmux-terminal.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []bool
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	actual := []bool{}
	for _, row := range rows {
		actual = append(actual, IsTmuxITerm2(row))
	}
	if !reflect.DeepEqual(actual, source) {
		t.Fatal(actual, source)
	}
}
