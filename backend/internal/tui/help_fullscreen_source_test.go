package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

func TestTerminalFullscreenHelpWholeOriginalModalAndOuterScrollConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original fullscreen Help required")
	}
	type keyStep struct{ input, name string }
	keys := []keyStep{{"\t", "tab"}, {"\t", "tab"}, {"\x1b[B", "down"}, {"\x1b[5~", "pageup"}, {"\x1b[6~", "pagedown"}}
	requests := []map[string]any{}
	native := [][][]string{}
	scrolls := [][]int{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, size := range [][2]int{{100, 36}, {47, 36}, {24, 36}, {100, 80}} {
			commands := []protocol.JSONObject{presentationObject([]byte(`{"name":"help","description":"Show help and available commands","builtin":true}`))}
			original := []map[string]any{}
			for i := 0; i < 12; i++ {
				name := fmt.Sprintf("owned-%02d", i)
				description := fmt.Sprintf("Actual description of custom command %02d", i)
				commands = append(commands, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: helpListTextJSON(name)}, protocol.JSONField{Name: "description", Value: helpListTextJSON(description + " (user)")}, protocol.JSONField{Name: "builtin", Value: []byte("false")}))
				original = append(original, map[string]any{"name": name, "description": description, "type": "prompt", "source": "userSettings"})
			}
			s := NewState(nil, size[0], size[1])
			s.fullscreen = true
			s.PermissionMode = "bypassPermissions"
			s.Status = "Working…"
			s.Queue = []Input{{Text: "pending human", Mode: "prompt"}}
			s.Preferences = TerminalPreferences{Theme: theme, ShowTips: true, SpinnerTip: "actual hidden prompt tip"}
			body := []string{}
			for i := 0; i < 70; i++ {
				body = append(body, fmt.Sprintf("SOURCE_TRANSCRIPT_%03d", i))
			}
			s.Entries = []Entry{{Kind: "bash_output", Text: strings.Join(body, "\n")}}
			data, _ := json.Marshal(TerminalHelpData{Version: "999.0.0-local", Commands: commands, Shortcuts: TerminalShortcutHelp{Platform: "macos"}})
			s.openLocalPanel(&LocalPanel{ID: "actual-fullscreen-help", Kind: "help", HelpDialog: true, Presentation: data})
			paint := [][]string{strings.Split(s.View("", true).Content, "\n")}
			positions := []int{s.Scroll}
			inputs := []string{}
			for _, key := range keys {
				if actions := s.HandleKey(Key{Name: key.name}, time.Now()); len(actions) != 0 || s.LocalPanel == nil || s.Closing {
					t.Fatal("Readonly modal navigation launched or closed a command", actions)
				}
				inputs = append(inputs, key.input)
				paint = append(paint, strings.Split(s.View("", true).Content, "\n"))
				positions = append(positions, s.Scroll)
			}
			inputs = append(inputs, "\x1b")
			requests = append(requests, map[string]any{"width": size[0], "height": size[1], "theme": theme, "commands": original, "keys": inputs})
			native, scrolls = append(native, paint), append(scrolls, positions)
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-fullscreen.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FULLSCREEN_HELP_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": requests})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("Original fullscreen root", err, string(output))
	}
	var source []struct {
		FullscreenEnabled bool
		Completion        struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
		}
		Snapshots []struct {
			Rows                                      []string
			AltScreen, Settled                        bool
			TranscriptTop, TranscriptHeight, ModalTop int
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source) != len(requests) {
		t.Fatal(err, string(output))
	}
	for i, row := range source {
		if !row.FullscreenEnabled || row.Completion.ShouldQuery || len(row.Completion.Messages) != 0 || len(row.Snapshots) != len(native[i])+1 {
			t.Fatal("Original actual fullscreen caller completion", i, row.Completion)
		}
		for j, frame := range row.Snapshots[:len(native[i])] {
			if !frame.AltScreen || frame.Settled || frame.ModalTop != 0 {
				t.Fatal("Original modal mount/inner scroll consumer", i, j, frame)
			}
			modal := 0
			for k, line := range frame.Rows {
				if strings.Contains(ansi.Strip(line), "▔") {
					modal = k
					break
				}
			}
			want := strings.Join(frame.Rows[modal:], "\n")
			gotRows := native[i][j]
			if len(gotRows) < len(frame.Rows) {
				gotRows = append(gotRows, make([]string, len(frame.Rows)-len(gotRows))...)
			}
			got := strings.Join(gotRows[modal:], "\n")
			if !reflect.DeepEqual(terminalFullscreenHelpCells(want, requests[i]["width"].(int)), terminalFullscreenHelpCells(got, requests[i]["width"].(int))) {
				t.Errorf("profile%d frame%d original fullscreen modal Source%q Go%q", i, j, ansi.Strip(want), ansi.Strip(got))
			}
		}
		previous, up, down := row.Snapshots[3], row.Snapshots[4], row.Snapshots[5]
		step := max(1, previous.TranscriptHeight/2)
		if up.TranscriptTop != max(0, previous.TranscriptTop-step) || down.TranscriptTop != previous.TranscriptTop || scrolls[i][4] != previous.TranscriptTop-up.TranscriptTop || scrolls[i][5] != 0 {
			t.Error("Original fullscreen Page keys must scroll outer transcript, retain Help selection/inner scroll", i, step, previous.TranscriptTop, up.TranscriptTop, down.TranscriptTop, scrolls[i])
		}
	}
}

func terminalFullscreenHelpCells(value string, width int) [][]terminalStatusLineCell {
	rows := [][]terminalStatusLineCell{}
	for _, line := range strings.Split(value, "\n") {
		cells := terminalStatusLineParse(line, false, "39")
		for len(cells) < width {
			cells = append(cells, terminalStatusLineCell{text: " ", style: terminalStatusLineStyle{fg: "39"}})
		}
		rows = append(rows, cells)
	}
	return rows
}
