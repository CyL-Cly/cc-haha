package clibootstrap

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

func TestActualChalkGenericTmuxColorFlagsEnvironmentAndTTY(t *testing.T) {
	type row struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
		TTY  bool              `json:"tty"`
	}
	rows := []row{}
	for _, tty := range []bool{false, true} {
		for _, env := range []map[string]string{{}, {"TERM": "xterm-256color"}, {"TERM": "dumb"}, {"FORCE_COLOR": "0"}, {"FORCE_COLOR": "1"}, {"FORCE_COLOR": "true"}, {"FORCE_COLOR": "false"}, {"FORCE_COLOR": ""}, {"FORCE_COLOR": "garbage"}, {"TERM_PROGRAM": "iTerm.app"}, {"CI": "1"}, {"GITHUB_ACTIONS": "1", "CI": ""}, {"COLORTERM": ""}, {"TERM_PROGRAM": "Apple_Terminal"}} {
			rows = append(rows, row{[]string{}, env, tty})
		}
	}
	for _, a := range [][]string{{"--color"}, {"--color=false"}, {"--color=always"}, {"--no-colors"}, {"--color=16m"}, {"--color=256"}, {"--", "--color"}} {
		rows = append(rows, row{a, map[string]string{"TERM": "xterm"}, true})
	}
	rows = append(rows, row{[]string{"--no-color"}, map[string]string{"FORCE_COLOR": "1"}, true})
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/tmux-color.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
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
	for _, r := range rows {
		value, e := nativeTmuxColor(r.Args, r.Env, "darwin", r.TTY)
		if e != nil {
			t.Fatal(e)
		}
		actual = append(actual, value)
	}
	if !reflect.DeepEqual(actual, source) {
		t.Fatal("actual installed color primitive", actual, source)
	}
	sourceBytes, e := os.ReadFile("../../../node_modules/chalk/source/vendor/supports-color/index.js")
	if e != nil {
		t.Fatal(e)
	}
	expected := []string{}
	for _, line := range strings.Split(string(sourceBytes), "\n") {
		if !strings.HasPrefix(line, "import ") {
			expected = append(expected, line)
		}
	}
	code := strings.Join(expected, "\n")
	code = strings.ReplaceAll(code, "export function createSupportsColor", "function createSupportsColor")
	code = strings.ReplaceAll(code, "export default supportsColor;", "supportsColor;")
	if !strings.HasSuffix(tmuxSupportsColor, strings.TrimRight(code, "\n")+"\n") {
		t.Fatal("vendored generic color primitive differs from frozen installed library")
	}
}
