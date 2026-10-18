package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type terminalSpinnerSourceFrame struct {
	Now, AnimationTime   int64
	ParentCommits        int
	ResponseLength       int
	StreamMode           string
	VisibleStreamingText *string
	ShowSpinner          bool
	Rows, PaintRows      []string
	AnimationInterval    int
	IntervalRequests     []int
}

func terminalSpinnerOriginalFrames(t *testing.T, reduced bool, rows []map[string]any) [][]terminalSpinnerSourceFrame {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original mounted Spinner and shared clock required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	input, err := json.Marshal(map[string]any{"home": home, "settings": map[string]any{"prefersReducedMotion": reduced, "spinnerTipsEnabled": false}, "rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original mounted REPL stream callback and actual createClock", err, string(output))
	}
	var original struct {
		Results [][]terminalSpinnerSourceFrame
		Caps    struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original.Results) != len(rows) || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatal(err, string(output))
	}
	return original.Results
}

func terminalSpinnerPublicRegion(s *State, count int, message string) []string {
	rows := strings.Split(s.View("", false).Content, "\n")
	start := -1
	for i, row := range rows {
		if strings.Contains(ansi.Strip(row), message) {
			start = i
			if i > 0 && strings.TrimSpace(ansi.Strip(rows[i-1])) == "" {
				start--
			}
			break
		}
	}
	if start < 0 {
		return nil
	}
	return rows[start:min(len(rows), start+count)]
}

func terminalSpinnerFixedElapsedPrimitive(s *State, base time.Time) {
	// These renderer profiles control the same three clock refs as their
	// original Source rows. Apply's real permission-clock synchronization is
	// tested separately; it must not reinterpret this paused OS primitive as
	// a mounted permission dialog or use post-Bun wall time in the frame.
	clock := s.ensureTerminalSpinnerClock()
	clock.started, clock.pausedAt, clock.paused = base.Add(-31500*time.Millisecond), base, 0
	s.dreamClock = base
}

// The original root callbacks own the length ref and streaming-text state.
// The original shared clock owns child animation. A second View read is only
// a read: it must not perform another token-counter increment.
func TestActualMountedSourceSpinnerRefOnlyParentCommitAndVisibleStreamingText(t *testing.T) {
	events := []json.RawMessage{
		[]byte(`{"type":"stream_request_start"}`),
		[]byte(`{"type":"stream_event","ttftMs":12,"event":{"type":"message_start","message":{"id":"actual-stream"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"A😀\ud800"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"does-not-change-state-or-length"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"actual-tool","name":"Read","input":{}}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":9,"delta":{"type":"input_json_delta","partial_json":"unmatched😀"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"中文\"}"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"partial text"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"\nnext partial"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"message_stop"}}`),
		[]byte(`{"type":"assistant","uuid":"actual-complete","message":{"role":"assistant","content":[{"type":"text","text":"complete"}]}}`),
	}
	steps := []map[string]any{}
	for index, event := range events {
		steps = append(steps, map[string]any{"streamEvent": event})
		if index == 3 {
			steps = append(steps, map[string]any{"animationTick": 100})
		}
	}
	for _, reduced := range []bool{false, true} {
		row := map[string]any{"tasks": []any{}, "animationClock": true, "streamConsumer": true, "loading": true, "width": 100, "height": 36, "message": "Actual root stream", "verbose": true, "elapsedMs": 31500, "pauseElapsedMs": 31500, "global": map[string]any{"btwUseCount": 1}, "steps": steps}
		frames := terminalSpinnerOriginalFrames(t, reduced, []map[string]any{row})[0]
		if len(frames) != len(steps)+1 {
			t.Fatal("Original stream did not reach every callback", len(frames))
		}
		// These are real mounted parent commits, not an inferred list of
		// event names. Thinking/signature/unmatched JSON retain the commit.
		for _, index := range []int{4, 5, 6, 8} {
			if frames[index].ParentCommits != frames[index-1].ParentCommits {
				t.Fatalf("Source ref-only profile reduced=%v step%d unexpectedly committed parent: %+v", reduced, index, frames)
			}
		}
		s := NewState(nil, 100, 36)
		s.ActiveTurn, s.Status = "spinner-stream", "Actual root stream…"
		s.Preferences = TerminalPreferences{Theme: "dark", ReducedMotion: reduced, Verbose: true}
		clock := s.ensureTerminalSpinnerClock()
		clock.verb, clock.verbKnown, clock.loading = "Actual root stream", true, true
		base := time.UnixMilli(frames[0].Now)
		clock.started, clock.pausedAt = base.Add(-31500*time.Millisecond), base
		s.dreamClock = base
		model := &terminalModel{ctx: context.Background(), state: s}
		for index, frame := range frames {
			if index > 0 {
				step := steps[index-1]
				if raw, ok := step["streamEvent"].(json.RawMessage); ok {
					message := presentationObject(raw)
					progress := TerminalSpinnerProgress{QueryEvent: raw}
					if message.String("type") == "stream_request_start" {
						progress.Mode = "requesting"
					} else if message.String("type") == "stream_event" {
						parsed, _, err := TerminalSpinnerStreamProgress(message.Get("event"))
						if err != nil {
							t.Fatal(err)
						}
						progress.Mode, progress.AddedResponseUnits = parsed.Mode, parsed.AddedResponseUnits
					}
					s.Apply(Event{Kind: TerminalSpinnerProgressChanged, TurnID: s.ActiveTurn, Spinner: &progress})
				} else {
					model.Update(terminalSpinnerAnimationMsg{now: time.UnixMilli(frame.Now), clockTime: time.Duration(frame.AnimationTime) * time.Millisecond})
				}
			}
			terminalSpinnerFixedElapsedPrimitive(s, base)
			if clock.mode != frame.StreamMode || clock.responseUnits != frame.ResponseLength {
				t.Errorf("Source stream reduced=%v step%d mode/UTF16 ref: Source %s/%d Go %s/%d", reduced, index, frame.StreamMode, frame.ResponseLength, clock.mode, clock.responseUnits)
			}
			count := len(frame.PaintRows)
			got := terminalSpinnerPublicRegion(s, max(2, count), "Actual root stream")
			if !frame.ShowSpinner {
				got = terminalSpinnerPublicRegion(s, 2, "Actual root stream")
			}
			if frame.ShowSpinner && !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, s.Width), terminalTaskListCells(got, s.Width)) || !frame.ShowSpinner && len(got) != 0 {
				t.Errorf("Original root stream reduced=%v step%d parentCommit%d: Source %q Go %q", reduced, index, frame.ParentCommits, frame.Rows, got)
			}
			if again := terminalSpinnerPublicRegion(s, max(2, count), "Actual root stream"); !reflect.DeepEqual(got, again) {
				t.Errorf("Second View read advanced spinner reduced=%v step%d: first %q second %q", reduced, index, got, again)
			}
		}
	}
}

func TestActualMountedSourceSpinnerSharedAnimationFramesSixThemes(t *testing.T) {
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, mode := range []string{"responding", "requesting", "tool-use"} {
			rows = append(rows, map[string]any{"tasks": []any{}, "theme": theme, "width": 100, "height": 36, "loading": true, "animationClock": true, "mode": mode, "message": "Animation 中文😀", "verbose": true, "elapsedMs": 31500, "pauseElapsedMs": 31500, "global": map[string]any{"btwUseCount": 1}, "steps": []map[string]any{{"streamLength": 500}, {"animationTick": 100}, {"animationTick": 150}, {"animationTick": 200}, {"animationTick": 3100}, {"animationTick": 5050}, {"props": map[string]any{"hasActiveTools": true}}}})
		}
	}
	all := terminalSpinnerOriginalFrames(t, false, rows)
	for index, row := range rows {
		frames := all[index]
		s := NewState(nil, 100, 36)
		s.ActiveTurn, s.Status = "animation-query", "Animation 中文😀…"
		s.Preferences = TerminalPreferences{Theme: row["theme"].(string), Verbose: true}
		clock := s.ensureTerminalSpinnerClock()
		clock.mode, clock.verb, clock.verbKnown, clock.loading = row["mode"].(string), "Animation 中文😀", true, true
		base := time.UnixMilli(frames[0].Now)
		clock.started, clock.pausedAt = base.Add(-31500*time.Millisecond), base
		s.dreamClock = base
		model := &terminalModel{ctx: context.Background(), state: s}
		for step, frame := range frames {
			if step == 1 {
				s.Apply(Event{Kind: TerminalSpinnerProgressChanged, TurnID: s.ActiveTurn, Spinner: &TerminalSpinnerProgress{AddedResponseUnits: 500}})
			} else if step > 1 && step < len(frames)-1 {
				model.Update(terminalSpinnerAnimationMsg{now: time.UnixMilli(frame.Now), clockTime: time.Duration(frame.AnimationTime) * time.Millisecond})
			} else if step == len(frames)-1 {
				s.Apply(Event{Kind: ToolStarted, TurnID: s.ActiveTurn, ToolID: "actual-active", ToolName: "Read", Input: json.RawMessage(`{"file_path":"private.txt"}`)})
			}
			terminalSpinnerFixedElapsedPrimitive(s, base)
			got := terminalSpinnerPublicRegion(s, len(frame.PaintRows), "Animation 中文😀")
			if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, s.Width), terminalTaskListCells(got, s.Width)) {
				t.Errorf("Original shared-clock profile%d step%d mode%s theme%s time%d: Source %q Go %q", index, step, clock.mode, s.Preferences.Theme, frame.AnimationTime, frame.Rows, got)
			}
			if again := terminalSpinnerPublicRegion(s, len(frame.PaintRows), "Animation 中文😀"); !reflect.DeepEqual(got, again) {
				t.Errorf("Second View read advanced shared-clock profile%d step%d", index, step)
			}
		}
	}
}

func TestActualMountedSourceSpinnerWholeClockProviderFocusAndMinimumInterval(t *testing.T) {
	rows := []map[string]any{}
	for _, item := range []struct{ mode, restore string }{{"responding", "\x1b[I"}, {"tool-use", "x"}} {
		rows = append(rows, map[string]any{"tasks": []any{}, "animationClock": true, "providerClock": true, "loading": true, "width": 100, "height": 36, "mode": item.mode, "message": "FRAME16 source " + item.mode, "verbose": true, "elapsedMs": 31500, "pauseElapsedMs": 31500, "global": map[string]any{"btwUseCount": 1}, "steps": []any{map[string]any{"streamLength": 500}, map[string]any{"animationTick": 49}, map[string]any{"animationTick": 64}, "\x1b[O", map[string]any{"animationTick": 128}, item.restore, map[string]any{"animationTick": 192}}})
	}
	all := terminalSpinnerOriginalFrames(t, false, rows)
	for index, row := range rows {
		frames := all[index]
		if len(frames) != 8 || !reflect.DeepEqual(frames[7].IntervalRequests, []int{16, 32, 16}) || frames[4].AnimationInterval != 32 || frames[7].AnimationInterval != 16 {
			t.Fatalf("Original whole ClockProvider/focus consumer differs: %+v", frames)
		}
		if !reflect.DeepEqual(frames[0].PaintRows, frames[1].PaintRows) || !reflect.DeepEqual(frames[1].PaintRows, frames[2].PaintRows) {
			t.Fatal("Original ref-only change or sub-50ms tick repainted child")
		}
		s := NewState(nil, 100, 36)
		message := row["message"].(string)
		s.ActiveTurn, s.Status = "clock-focus-query", message+"…"
		s.Preferences = TerminalPreferences{Theme: "dark", Verbose: true}
		clock := s.ensureTerminalSpinnerClock()
		clock.mode, clock.verb, clock.verbKnown, clock.loading = row["mode"].(string), message, true, true
		base := time.UnixMilli(frames[0].Now)
		clock.started, clock.pausedAt = base.Add(-31500*time.Millisecond), base
		s.dreamClock = base
		model := &terminalModel{ctx: context.Background(), state: s}
		for step, frame := range frames {
			switch step {
			case 1:
				s.Apply(Event{Kind: TerminalSpinnerProgressChanged, TurnID: s.ActiveTurn, Spinner: &TerminalSpinnerProgress{AddedResponseUnits: 500}})
			case 2, 3, 5, 7:
				model.Update(terminalSpinnerAnimationMsg{now: time.UnixMilli(frame.Now), clockTime: time.Duration(frame.AnimationTime) * time.Millisecond})
			case 4:
				model.Update(tea.BlurMsg{})
			case 6:
				if index == 0 {
					model.Update(tea.FocusMsg{})
				} else {
					model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
				}
			}
			terminalSpinnerFixedElapsedPrimitive(s, base)
			got := terminalSpinnerPublicRegion(s, len(frame.PaintRows), message)
			if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, s.Width), terminalTaskListCells(got, s.Width)) {
				t.Errorf("Original whole ClockProvider profile%d step%d interval%d: Source %q Go %q", index, step, frame.AnimationInterval, frame.Rows, got)
			}
		}
	}
}
