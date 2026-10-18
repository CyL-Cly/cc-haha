package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualOriginalRootHandlersWithMountedHelpGlobalConflictsAndChords(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original mounted root consumer required")
	}
	type step struct{ wire, key string }
	type profile struct {
		fullscreen bool
		steps      []step
		bindings   []map[string]any
	}
	profiles := []profile{
		{false, []step{{"\x14", "ctrl+t"}, {"\x14", "ctrl+t"}, {"\x0f", "ctrl+o"}, {"\x05", "ctrl+e"}, {"\x1b", "esc"}, {"\x1b", "esc"}}, nil},
		{true, []step{{"\x0f", "ctrl+o"}, {"\x05", "ctrl+e"}, {"\x1b", "esc"}, {"\x1b", "esc"}}, nil},
		{false, []step{{"\x18", "ctrl+x"}, {"\x0b", "ctrl+k"}, {"\x1b", "esc"}}, nil},
		{true, []step{{"\x1b[79;6u", "ctrl+shift+o"}, {"\x1b[79;6u", "ctrl+shift+o"}, {"\x0c", "ctrl+l"}, {"\x1bj", "meta+j"}, {"\x1b", "esc"}}, nil},
		{false, []step{{"\x14", "ctrl+t"}, {"\x1b", "esc"}, {"\x1b", "esc"}}, []map[string]any{{"context": "Global", "bindings": map[string]any{"ctrl+t": "app:toggleTranscript"}}, {"context": "Help", "bindings": map[string]any{"ctrl+t": "help:dismiss"}}}},
		{false, []step{{"\x18", "ctrl+x"}, {"\x0b", "ctrl+k"}, {"\x1b", "esc"}, {"\x1b", "esc"}}, []map[string]any{{"context": "Global", "bindings": map[string]any{"ctrl+x ctrl+k": "app:toggleTranscript"}}}},
		{false, []step{{"\x14", "ctrl+t"}}, []map[string]any{{"context": "Global", "bindings": map[string]any{"ctrl+t": nil}}, {"context": "Help", "bindings": map[string]any{"ctrl+t": "help:dismiss"}}}},
		{true, []step{{"\x14", "ctrl+t"}, {"\x1b", "esc"}}, []map[string]any{{"context": "Global", "bindings": map[string]any{"ctrl+t": "command:model"}}}},
	}
	rows := []map[string]any{}
	for _, p := range profiles {
		inputs := []string{}
		for _, step := range p.steps {
			inputs = append(inputs, step.wire)
		}
		rows = append(rows, map[string]any{"help": true, "fullscreen": p.fullscreen, "steps": inputs, "bindings": p.bindings})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-root-shortcuts.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ROOT_SHORTCUTS_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3", "PATH=/usr/bin:/bin", "USER_TYPE=ant"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original whole root consumer", err, string(output))
	}
	type frame struct {
		Screen, ExpandedView, Notifications               string
		Preview, Settled, ShowAllInTranscript, Fullscreen bool
		Commands                                          []json.RawMessage
		Rows                                              []string
	}
	var source struct {
		Results  [][]frame
		Bindings [][]Keybinding
		Blocked  []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(profiles) || len(source.Bindings) != len(profiles) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for i, p := range profiles {
		s := NewState(nil, 100, 36)
		s.fullscreen = p.fullscreen
		s.ShowTeammatePreview = source.Results[i][0].Preview
		s.Notification = source.Results[i][0].Notifications
		s.SetKeybindings(source.Bindings[i])
		s.openLocalPanel(&LocalPanel{ID: "root-help", Kind: "help", HelpDialog: true, Presentation: []byte(`{"version":"999.0.0-local","commands":[],"shortcuts":{"platform":"macos","keybindingsEnabled":true}}`)})
		for j, want := range source.Results[i] {
			if j > 0 {
				key := Key{Name: p.steps[j-1].key}
				stroke := ParseKeystroke(key.Name)
				key.Stroke = &stroke
				actions := s.HandleKey(key, time.Unix(200, 0).Add(time.Duration(j)*80*time.Millisecond))
				for _, action := range actions {
					if action.kind == actionInvoke && action.name != "app:refreshTodos" {
						t.Errorf("profile%d step%d unexpected invocation %s", i, j, action.name)
					}
				}
			}
			screen := "prompt"
			if s.Transcript {
				screen = "transcript"
			}
			expanded := "none"
			if s.Teammates != nil {
				expanded = s.Teammates.ExpandedView
			}
			if screen != want.Screen || expanded != want.ExpandedView || s.ShowTeammatePreview != want.Preview || (s.LocalPanel == nil) != want.Settled || s.Notification != want.Notifications || s.transcriptShowAll != want.ShowAllInTranscript {
				t.Errorf("profile%d frame%d original root %s/%s/preview%v/settled%v/notice%q Go %s/%s/preview%v/settled%v/notice%q", i, j, want.Screen, want.ExpandedView, want.Preview, want.Settled, want.Notifications, screen, expanded, s.ShowTeammatePreview, s.LocalPanel == nil, s.Notification)
			}
			if len(want.Commands) != 0 {
				t.Fatal("Original command shortcut ran under local JSX", i, j, want.Commands)
			}
		}
	}
}

func TestActualOriginalRootTranscriptWithStillMountedHelpPaint(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original root paint required")
	}
	rows := []map[string]any{}
	for _, width := range []int{100, 47, 24} {
		rows = append(rows, map[string]any{"width": width, "fullscreen": true, "help": true, "capturePaint": true, "history": append(func() []string {
			rows := []string{}
			for i := 0; i < 70; i++ {
				rows = append(rows, fmt.Sprintf("ROOT_TRANSCRIPT_%03d", i))
			}
			return rows
		}(), " "), "steps": []string{"\x0f", "\x05", "\x0f", "\x1b"}})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-root-shortcuts.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ROOT_SHORTCUTS_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3", "PATH=/usr/bin:/bin", "USER_TYPE=ant"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Results [][]struct {
			Screen    string
			PaintRows []string
		}
		Bindings [][]Keybinding
		Blocked  []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		s := NewState(nil, row["width"].(int), 36)
		s.fullscreen = true
		s.Preferences.Theme = "dark"
		s.SetKeybindings(source.Bindings[i])
		body := []string{}
		for j := 0; j < 70; j++ {
			body = append(body, fmt.Sprintf("ROOT_TRANSCRIPT_%03d", j))
		}
		s.Entries = []Entry{{Kind: "bash_output", Text: strings.Join(body, "\n")}}
		s.openLocalPanel(&LocalPanel{ID: "root-transcript-help", Kind: "help", HelpDialog: true, Presentation: []byte(`{"version":"999.0.0-local","commands":[],"shortcuts":{"platform":"macos","keybindingsEnabled":true}}`)})
		s.HandleKey(Key{Name: "ctrl+o"}, time.Unix(500, 0))
		frame := source.Results[i][1]
		want := strings.Join(frame.PaintRows, "\n")
		got := s.View("", true).Content
		if !reflect.DeepEqual(terminalFullscreenHelpCells(want, s.Width), terminalFullscreenHelpCells(got, s.Width)) {
			wc, gc := terminalFullscreenHelpCells(want, s.Width), terminalFullscreenHelpCells(got, s.Width)
			shown := 0
			for y := range wc {
				for x := range wc[y] {
					if y < len(gc) && x < len(gc[y]) && !reflect.DeepEqual(wc[y][x], gc[y][x]) && shown < 8 {
						t.Logf("cell%d,%d Source%+v Go%+v", x, y, wc[y][x], gc[y][x])
						shown++
					}
				}
			}
			t.Errorf("width%d original root transcript+Help paint Source%q Go%q", s.Width, ansi.Strip(want), ansi.Strip(got))
		}
	}
}

func TestActualOriginalRootSettingsEnvironmentFlipsRemountOnlyHelpUI(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original mounted settings subscriber required")
	}
	flag := func(value string) map[string]any {
		return map[string]any{"settings": map[string]any{"env": map[string]string{"CLAUDE_CODE_NO_FLICKER": value}}}
	}
	rows := []map[string]any{
		{"help": true, "fullscreen": false, "steps": []any{"\t", "\x1b[B", flag("1"), "\t", flag("0"), "\x1b"}},
		{"help": true, "fullscreen": true, "steps": []any{"\t", flag("0"), flag("1"), "\x1b"}},
		{"help": true, "fullscreen": true, "steps": []any{"\x0f", flag("0"), "\x05", "\x1b", "\x1b"}},
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-root-shortcuts.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ROOT_SHORTCUTS_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3", "PATH=/usr/bin:/bin", "USER_TYPE=ant"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Results [][]struct {
			Fullscreen, AltScreen, Settled, ShowAllInTranscript bool
			Screen                                              string
			Rows                                                []string
			Completion                                          struct{ Messages []json.RawMessage }
		}
		Bindings [][]Keybinding
		Blocked  []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		s := NewState(nil, 100, 36)
		s.fullscreen = row["fullscreen"].(bool)
		s.SetKeybindings(source.Bindings[i])
		s.ActiveTurn = "actual-held-local-turn"
		s.Queue = []Input{{Text: "actual pending next human", Mode: "prompt"}}
		s.pendingLocalCommands = map[uint64]struct{}{91: {}}
		s.openLocalPanel(&LocalPanel{ID: "current-settings-help", Kind: "help", HelpDialog: true, Presentation: []byte(`{"version":"999.0.0-local","commands":[{"name":"help","builtin":true}],"shortcuts":{"platform":"macos","keybindingsEnabled":true}}`)})
		m := &terminalModel{state: s, options: Options{Fullscreen: s.fullscreen}}
		steps := row["steps"].([]any)
		for j, want := range source.Results[i] {
			if j > 0 {
				switch step := steps[j-1].(type) {
				case string:
					name := map[string]string{"\t": "tab", "\x1b[B": "down", "\x0f": "ctrl+o", "\x05": "ctrl+e", "\x1b": "esc"}[step]
					s.HandleKey(Key{Name: name}, time.Unix(900, 0).Add(time.Duration(j)*80*time.Millisecond))
				case map[string]any:
					env := step["settings"].(map[string]any)["env"].(map[string]string)
					m.applyTerminalFullscreen(env["CLAUDE_CODE_NO_FLICKER"] == "1")
				}
			}
			if s.fullscreen != want.Fullscreen || m.options.Fullscreen != want.AltScreen || s.Transcript != (want.Screen == "transcript") || s.transcriptShowAll != want.ShowAllInTranscript || (s.LocalPanel == nil) != want.Settled {
				t.Errorf("profile%d frame%d original settings/root projection differs", i, j)
			}
			if s.LocalPanel != nil {
				general := strings.Contains(strings.Join(want.Rows, "\n"), "Claude understands your codebase")
				if general != (s.LocalPanel.terminalHelpTab() == "general") {
					t.Errorf("profile%d frame%d real AlternateScreen remount must reset Help tab", i, j)
				}
			}
			if s.ActiveTurn != "actual-held-local-turn" || len(s.Queue) != 1 || len(s.pendingLocalCommands) != 1 {
				t.Fatal("Display remount changed canonical completion reservation/queued human", i, j)
			}
		}
		last := source.Results[i][len(source.Results[i])-1]
		expected := 2
		if last.Fullscreen {
			expected = 0
		}
		if len(last.Completion.Messages) != expected {
			t.Fatal("Original complete processSlash uses current mode for canonical completion", i, last.Completion)
		}
	}
}
