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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

// This exercises the public State.View consumer, not a duplicate of the
// header algorithm. The original mounted REPL chooses SpinnerWithVerb and
// its actual Ink children supply the complete header cells and styles.
func TestActualMountedSourceSpinnerHeaderSixThemesWidthModesAndTaskVerb(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original mounted Spinner required")
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			for _, mode := range []string{"requesting", "thinking", "responding"} {
				rows = append(rows, map[string]any{"tasks": []any{}, "theme": theme, "width": width, "height": 36, "loading": true, "mode": mode, "message": "Working", "responseLength": 4940, "elapsedMs": 31500, "pauseElapsedMs": 31500, "verbose": true, "global": map[string]any{"btwUseCount": 1}})
			}
		}
	}
	for _, values := range []map[string]any{
		{"width": 24, "message": "Long message 中文😀 several words"},
		{"width": 12, "message": "Wrap 漢字😀"},
		{"width": 4, "message": "AB"},
		{"spinnerSuffix": "Running stop hooks", "width": 24},
		{"tasks": []any{map[string]any{"subject": "Current subject", "activeForm": "Active form", "status": "in_progress"}, map[string]any{"subject": "Next actual task", "status": "pending"}}, "sourceMessage": true, "message": nil},
	} {
		row := map[string]any{"tasks": []any{}, "theme": "dark", "width": 100, "height": 36, "loading": true, "mode": "responding", "message": "Working", "responseLength": 4000, "elapsedMs": 500, "pauseElapsedMs": 500, "verbose": false, "global": map[string]any{"btwUseCount": 1}}
		for key, value := range values {
			row[key] = value
		}
		rows = append(rows, row)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	input, _ := json.Marshal(map[string]any{"home": home, "settings": map[string]any{"prefersReducedMotion": true, "spinnerTipsEnabled": false}, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original REPL / SpinnerWithVerb mounted consumer", err, string(output))
	}
	var original struct {
		Results [][]struct {
			Props     protocol.JSONObject
			Tasks     []protocol.JSONObject
			Now       int64
			PaintRows []string
			Rows      []string
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Results) != len(rows) || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatal(err, string(output))
	}
	for index, frames := range original.Results {
		frame := frames[0]
		s := NewState(nil, rows[index]["width"].(int), rows[index]["height"].(int))
		s.Preferences = TerminalPreferences{Theme: rows[index]["theme"].(string), ReducedMotion: true, Verbose: rows[index]["verbose"].(bool)}
		s.ActiveTurn = "actual-spinner-query"
		s.TaskItems = frame.Tasks
		s.dreamClock = time.UnixMilli(frame.Now)
		elapsed := rows[index]["elapsedMs"].(int)
		clock := s.ensureTerminalSpinnerClock()
		clock.mode = frame.Props.String("mode")
		clock.started = s.dreamClock.Add(-time.Duration(elapsed) * time.Millisecond)
		clock.pausedAt = s.dreamClock
		clock.responseUnits = rows[index]["responseLength"].(int)
		clock.loading = true
		clock.verb, clock.verbKnown = frame.Props.String("message"), frame.Props.Get("message") != nil && string(frame.Props.Get("message")) != "null"
		if clock.verbKnown {
			message := clock.verb
			clock.overrideMessage = &message
		}
		clock.suffix = frame.Props.String("spinnerSuffix")
		s.Status = "Working…"
		if clock.verbKnown {
			s.Status = clock.verb + "…"
		}
		view := strings.Split(s.View("", false).Content, "\n")
		// Locate the actual public spinner region while retaining its blank
		// top margin. Prompt/editor siblings remain in State.View.
		start := -1
		for i, row := range view {
			plain := ansi.Strip(row)
			if strings.HasPrefix(plain, "●") || strings.Contains(plain, "Working…") || strings.Contains(plain, "Long message") || strings.Contains(plain, "Wrap 漢字") || strings.Contains(plain, "AB…") || strings.Contains(plain, "Active form…") {
				start = i
				if i > 0 && strings.TrimSpace(ansi.Strip(view[i-1])) == "" {
					start--
				}
				break
			}
		}
		got := []string{}
		if start >= 0 {
			got = view[start:min(start+len(frame.PaintRows), len(view))]
		}
		if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, s.Width), terminalTaskListCells(got, s.Width)) {
			t.Errorf("mounted header profile%d mode%s width%d theme%s\nSource%q\nGo%q\nwholeView%q", index, clock.mode, s.Width, s.Preferences.Theme, frame.Rows, got, view)
		}
	}
}
