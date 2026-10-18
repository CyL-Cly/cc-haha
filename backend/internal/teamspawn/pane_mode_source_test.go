package teamspawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTerminalPaneInProcessPredicateAgainstActualOriginalCachedMode(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	actual := []bool{}
	for _, mode := range []string{"auto", "in-process", "tmux", ""} {
		for _, nonInteractive := range []bool{false, true} {
			for _, tmux := range []bool{false, true} {
				for _, iterm := range []bool{false, true} {
					for _, fallback := range []bool{false, true} {
						rows = append(rows, map[string]any{"mode": mode, "nonInteractive": nonInteractive, "tmux": tmux, "iterm": iterm, "fallback": fallback})
						env := map[string]string{}
						if tmux {
							env["TMUX"] = "actual-launch-tmux"
						}
						if iterm {
							env["TERM_PROGRAM"] = "iTerm.app"
						}
						p, err := NewPaneRuntime(home, os.Getpid(), env)
						if err != nil {
							t.Fatal(err)
						}
						if fallback {
							p.inProcessFallback = true
						}
						actual = append(actual, p.InProcessEnabled(mode, nonInteractive))
					}
				}
			}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-team-footer.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAM_FOOTER_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "modes": rows, "footers": []any{}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var expected struct{ Modes []bool }
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err, string(output))
	}
	if len(expected.Modes) != len(actual) {
		t.Fatal(string(output))
	}
	for i, value := range actual {
		if value != expected.Modes[i] {
			t.Fatal(i, rows[i], value, expected.Modes[i])
		}
	}
}
