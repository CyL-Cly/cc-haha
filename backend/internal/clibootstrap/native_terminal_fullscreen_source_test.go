package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestTerminalFullscreenDynamicEnvironmentAndMemoizedPhysicalTmuxMatchOriginal(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original fullscreen gate required")
	}
	type step struct {
		Environment map[string]string `json:"environment"`
		Output      string            `json:"output"`
		Error       bool              `json:"error"`
	}
	makeStep := func(tmux, program, term, flag, output string, failed bool) step {
		return step{map[string]string{"USER_TYPE": "ant", "TMUX": tmux, "TERM_PROGRAM": program, "TERM": term, "CLAUDE_CODE_NO_FLICKER": flag}, output, failed}
	}
	profiles := [][]step{
		{makeStep("owned", "", "xterm", "", "1\n", false), makeStep("", "tmux", "screen", "", "0\n", false), makeStep("owned", "", "xterm", "1", "0", false), makeStep("owned", "", "xterm", "0", "0", false)},
		{makeStep("owned", "", "xterm", "", "0\n", false), makeStep("owned", "", "xterm", "", "1\n", false)},
		{makeStep("owned", "", "xterm", "", "1\n", true), makeStep("owned", "", "xterm", "", "1\n", false)},
		{makeStep("", "", "xterm", "", "1\n", false), makeStep("owned", "", "xterm", "", "1\n", false)},
		{makeStep("owned", "iTerm.app", "xterm", "", "0\n", false), makeStep("owned", "iTerm.app", "screen", "", "0\n", false)},
		{makeStep("owned", "tmux", "screen", "", "1\n", false), makeStep("owned", "", "xterm", "", "1\n", false)},
		{makeStep("owned", "", "xterm", "1", "1\n", false), makeStep("owned", "", "xterm", "", "1\n", false)},
	}
	rows := []map[string]any{}
	for _, p := range profiles {
		rows = append(rows, map[string]any{"steps": p})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tui-history.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_HISTORY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "fullscreen": rows, "histories": []any{}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Fullscreen []struct {
			Steps []struct {
				Enabled bool
				Probes  int
			}
		}
		Blocked []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Fullscreen) != len(profiles) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "tmux")
			receipt := filepath.Join(root, "actual-probes")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$TMUX_RECEIPT\"\nprintf '%s' \"$TMUX_REPLY\"\nexit \"$TMUX_CODE\"\n"
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.Interactive = true })
			for j, current := range profile {
				var enabled bool
				err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					for key, value := range current.Environment {
						if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
							return err
						}
					}
					for key, value := range map[string]string{"PATH": root + ":/usr/bin:/bin", "TMUX_RECEIPT": receipt, "TMUX_REPLY": current.Output, "TMUX_CODE": map[bool]string{false: "0", true: "1"}[current.Error]} {
						if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
							return err
						}
					}
					var e error
					enabled, e = m.terminalFullscreenOnOwner(context.Background(), scope)
					return e
				})
				data, _ := os.ReadFile(receipt)
				probes := strings.Count(string(data), "display-message -p #{client_control_mode}\n")
				if err != nil || enabled != source.Fullscreen[i].Steps[j].Enabled || probes != source.Fullscreen[i].Steps[j].Probes {
					t.Fatal("actual native probe/dynamic environment differs from original memo", j, err, enabled, probes, source.Fullscreen[i].Steps[j], string(data))
				}
			}
		})
	}
}
