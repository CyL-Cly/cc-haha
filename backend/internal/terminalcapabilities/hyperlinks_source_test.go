package terminalcapabilities

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

func TestActualSourceHyperlinkLibraryAndInkConsumerMatrix(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	type profile struct {
		Env      map[string]string `json:"env"`
		Platform string            `json:"platform"`
		TTY      bool              `json:"tty"`
		Color    bool              `json:"color"`
		Args     []string          `json:"args"`
	}
	rows := []profile{}
	environments := []map[string]string{{}, {"FORCE_HYPERLINK": "0"}, {"FORCE_HYPERLINK": "1"}, {"FORCE_HYPERLINK": "false"}, {"FORCE_HYPERLINK": "\ufeff0"}, {"FORCE_HYPERLINK": "\u00850"}, {"NETLIFY": "1"}, {"WT_SESSION": ""}, {"CI": "1", "TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.72.0"}, {"TEAMCITY_VERSION": "2025"}, {"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.0"}, {"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.1"}, {"TERM_PROGRAM": "WezTerm", "TERM_PROGRAM_VERSION": "0-unstable-2020-06-19"}, {"TERM_PROGRAM": "WezTerm", "TERM_PROGRAM_VERSION": "0-unstable-2020-06-20"}, {"TERM_PROGRAM": "WezTerm", "TERM_PROGRAM_VERSION": "0-unstable-abcd-ab-ab"}, {"TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.71"}, {"TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.72"}, {"TERM_PROGRAM": "vscode", "CURSOR_TRACE_ID": "owned"}, {"TERM_PROGRAM": "ghostty"}, {"TERM_PROGRAM": "zed"}, {"TERM_PROGRAM": "alacritty"}, {"TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2"}, {"TERM": "xterm-kitty"}, {"VTE_VERSION": "0.50.0"}, {"VTE_VERSION": "5000"}, {"VTE_VERSION": "4601"}, {"VTE_VERSION": "0.51"}}
	for _, env := range environments {
		for _, platform := range []string{"darwin", "linux", "win32"} {
			for _, boundary := range [][2]bool{{false, false}, {false, true}, {true, false}, {true, true}} {
				for _, args := range [][]string{{}, {"--no-hyperlink"}, {"--hyperlink=always"}, {"--", "--hyperlink=always"}} {
					rows = append(rows, profile{env, platform, boundary[0], boundary[1], args})
				}
			}
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-setup.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_SETUP_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	data, _ := json.Marshal(map[string]any{"home": home, "hyperlinkRows": rows})
	command.Stdin = bytes.NewReader(data)
	data, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("full original hyperlink Source %v %.5000s", err, data)
	}
	var source struct {
		Values []struct{ Library, Extended bool }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Values) != len(rows) {
		t.Fatalf("invalid Source %v %.3000s", err, data)
	}
	for index, row := range rows {
		library := LibraryHyperlinks(row.Env, row.Platform, row.TTY, row.Color, row.Args)
		if library != source.Values[index].Library || SupportsHyperlinks(row.Env, library) != source.Values[index].Extended {
			t.Errorf("row%d %+v Go%v/%v Source%+v", index, row, library, SupportsHyperlinks(row.Env, library), source.Values[index])
		}
	}
}
