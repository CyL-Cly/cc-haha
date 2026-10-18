package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualOriginalREPLSpinnerClockPermissionTypingLocalJSXAndPauseResume(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual original REPL timing and focus consumers required")
	}
	props := func(value map[string]any) map[string]any { return map[string]any{"props": value} }
	advance := func(value int) map[string]any { return map[string]any{"advanceClock": value} }
	rows := []map[string]any{
		{"steps": []any{props(map[string]any{"loading": true}), advance(1000), props(map[string]any{"permission": true}), advance(5000), props(map[string]any{"permission": false}), advance(1000), props(map[string]any{"loading": false})}},
		{"loading": true, "steps": []any{props(map[string]any{"typing": true, "permission": true}), advance(2000), props(map[string]any{"typing": false}), advance(3000), props(map[string]any{"messageSelector": true}), advance(1000), props(map[string]any{"messageSelector": false})}},
		{"loading": true, "steps": []any{props(map[string]any{"question": true}), advance(1000), props(map[string]any{"question": false, "permission": true}), advance(2000), props(map[string]any{"permission": false})}},
		{"loading": true, "steps": []any{props(map[string]any{"localJSX": true, "permission": true}), advance(2000), props(map[string]any{"localJSX": false}), advance(3000), props(map[string]any{"localJSX": true})}},
		{"loading": true, "steps": []any{props(map[string]any{"permission": true}), advance(1500), props(map[string]any{"exiting": true}), advance(1500), props(map[string]any{"exiting": false})}},
	}
	for _, row := range rows {
		row["clockConsumer"], row["width"], row["height"] = true, 100, 36
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original mounted REPL timing refs, focus and Spinner", err, string(output))
	}
	var original struct {
		Results [][]struct {
			Now                int64
			Props              protocol.JSONObject
			FocusedInputDialog string
			Clock              struct {
				Started, Paused int64
				PauseStart      *int64
			}
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Results) != len(rows) || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatal(err, string(output))
	}
	for index, frames := range original.Results {
		s := NewState(nil, 100, 36)
		for step, frame := range frames {
			truthy := func(name string) bool { return string(frame.Props.Get(name)) == "true" }
			s.ActiveTurn = ""
			if truthy("loading") {
				s.ActiveTurn = "actual-spinner-clock"
			}
			s.Permissions = nil
			if truthy("permission") {
				s.Permissions = []PermissionRequest{{ID: "actual-permission", ToolName: "Read"}}
			} else if truthy("question") {
				s.Permissions = []PermissionRequest{{ID: "actual-question", ToolName: "AskUserQuestion"}}
			}
			s.LocalPanel = nil
			if truthy("localJSX") {
				s.LocalPanel = &localPanelState{props: LocalPanel{ID: "actual-local-jsx", OriginKnown: true, LocalJSXCommand: true}}
			}
			s.pluginHintPromptActive, s.Closing = truthy("typing"), truthy("exiting")
			s.MessageSelector = nil
			if truthy("messageSelector") {
				s.MessageSelector = &messageSelectorState{}
			}
			s.syncTerminalSpinnerClock(time.UnixMilli(frame.Now))
			clock := s.spinnerClock
			started, pauseStart := int64(0), int64(0)
			if !clock.started.IsZero() {
				started = clock.started.UnixMilli()
			}
			if !clock.pausedAt.IsZero() {
				pauseStart = clock.pausedAt.UnixMilli()
			}
			wantPauseStart := int64(0)
			if frame.Clock.PauseStart != nil {
				wantPauseStart = *frame.Clock.PauseStart
			}
			if started != frame.Clock.Started || clock.paused.Milliseconds() != frame.Clock.Paused || pauseStart != wantPauseStart || s.terminalSpinnerToolPermissionFocused() != (frame.FocusedInputDialog == "tool-permission") {
				t.Fatalf("Actual original spinner clock profile%d frame%d focus%s: Source%+v Native start%d paused%d pauseStart%d", index, step, frame.FocusedInputDialog, frame.Clock, started, clock.paused.Milliseconds(), pauseStart)
			}
		}
	}
}

func TestTerminalSpinnerProgressUsesCurrentTurnAndResetsOnlyOnTurnCompletion(t *testing.T) {
	s := NewState(nil, 100, 36)
	s.start(Input{TurnID: "actual-human", Text: "Actual source stream"})
	s.Apply(Event{TurnID: "old-retired-turn", Kind: TerminalSpinnerProgressChanged, Spinner: &TerminalSpinnerProgress{AddedResponseUnits: 99, Mode: "thinking"}})
	if s.spinnerClock.responseUnits != 0 || s.spinnerClock.mode != "responding" {
		t.Fatal("Late stream changed a different turn")
	}
	s.Apply(Event{TurnID: "actual-human", Kind: TerminalSpinnerProgressChanged, Spinner: &TerminalSpinnerProgress{AddedResponseUnits: 5, Mode: "thinking"}})
	s.Apply(Event{TurnID: "actual-human", Kind: TerminalSpinnerProgressChanged, Spinner: &TerminalSpinnerProgress{Mode: "requesting"}})
	if s.spinnerClock.responseUnits != 5 || s.spinnerClock.mode != "requesting" {
		t.Fatal("Next actual model round reset the response ref")
	}
	s.Apply(Event{TurnID: "actual-human", Kind: TextDelta, Text: "already counted original stream"})
	if s.spinnerClock.responseUnits != 5 {
		t.Fatal("Projected text was counted twice")
	}
	s.FinishTurn("actual-human", nil)
	if s.spinnerClock.responseUnits != 0 || s.spinnerClock.mode != "requesting" {
		t.Fatal("resetLoadingState changed mode or retained response units")
	}
}
