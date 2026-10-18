package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func key(s *State, name string) []action {
	return s.HandleKey(Key{Name: name}, time.Unix(100, 0))
}

func TestEnterMultilineAndPasteShareOneSubmission(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Insert("你好\\")
	if actions := key(s, "enter"); len(actions) != 0 || s.Editor.Text() != "你好\n" {
		t.Fatalf("backslash submitted: %v %q", actions, s.Editor.Text())
	}
	s.Editor.Insert("👍🏽")
	if actions := key(s, "shift+enter"); len(actions) != 0 {
		t.Fatal("Shift-Enter submitted")
	}
	s.Paste("第三行\r第四行")
	if s.Busy() {
		t.Fatal("paste executed a turn")
	}
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].kind != actionSubmit || actions[0].input.Text != "你好\n👍🏽\n第三行\n第四行" {
		t.Fatalf("submission lost bytes: %#v", actions)
	}
	if actions[0].input.Mode != "prompt" || actions[0].input.TurnID == "" {
		t.Fatal("submission lost host metadata")
	}
}

func TestQueueWaitsForActualSubmitReturnRatherThanFinalEvent(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("first")
	first := key(s, "enter")[0].input
	s.Editor.Set("second")
	if actions := key(s, "enter"); len(actions) != 0 || len(s.Queue) != 1 {
		t.Fatal("parallel second turn started")
	}
	s.Apply(Event{TurnID: first.TurnID, Kind: TurnFinished})
	if s.ActiveTurn != first.TurnID || len(s.Queue) != 1 {
		t.Fatal("final event bypassed host cleanup")
	}
	actions := s.FinishTurn(first.TurnID, nil)
	if len(actions) != 1 || actions[0].input.Text != "second" || len(s.Queue) != 0 {
		t.Fatalf("next owned turn missing: %#v", actions)
	}
	s.Apply(Event{TurnID: first.TurnID, Kind: TextDelta, Text: "late first"})
	if strings.Contains(s.View("", false).Content, "late first") {
		t.Fatal("late prior turn modified the next turn")
	}
}

func TestCancelPreservesPartialOutputAndRestoresQueuedEditableInput(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("first")
	first := key(s, "enter")[0].input
	s.Apply(Event{TurnID: first.TurnID, Kind: TextDelta, Text: "partial 中文"})
	s.Editor.Set("queued")
	key(s, "enter")
	s.Editor.Set("new draft")
	if actions := key(s, "esc"); len(actions) != 1 || actions[0].kind != actionCancel {
		t.Fatal("Esc did not cancel")
	}
	if actions := key(s, "esc"); len(actions) != 0 {
		t.Fatal("cancel issued twice")
	}
	if actions := s.FinishTurn(first.TurnID, errors.New("context canceled")); len(actions) != 0 {
		t.Fatal("cancel started queued turn")
	}
	if s.Editor.Text() != "queued\nnew draft" || len(s.Queue) != 0 {
		t.Fatalf("queued editable input lost: %q", s.Editor.Text())
	}
	if !strings.Contains(s.View("", false).Content, "partial 中文") || s.Status != "Request interrupted by user" {
		t.Fatal("partial cancellation display lost")
	}
}

func TestPermissionUsesOriginalPayloadAndCannotSubmitPrompt(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("write file")
	input := key(s, "enter")[0].input
	request := &PermissionRequest{ID: "permission-1", ToolName: "Write", Input: json.RawMessage(`{"file_path":"中文.txt","content":"hello"}`), Payload: json.RawMessage(`{"unknown":true}`)}
	s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: request})
	s.Editor.Set("draft")
	s.Paste("unsafe paste")
	if s.Editor.Text() != "draft" {
		t.Fatal("approval paste changed prompt")
	}
	actions := key(s, "n")
	if len(actions) != 1 || actions[0].kind != actionPermission || actions[0].permission != request.ID || actions[0].decision.Allowed {
		t.Fatal("denial did not reach the original request")
	}
	if string(request.Payload) != `{"unknown":true}` || string(request.Input) != `{"file_path":"中文.txt","content":"hello"}` {
		t.Fatal("rendering rewrote original payload")
	}
	s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: request})
	actions = key(s, "enter")
	if len(actions) != 1 || !actions[0].decision.Allowed || actions[0].kind == actionSubmit {
		t.Fatal("Enter submitted prompt instead of approving")
	}
}

func TestToolProgressAndResultStayAtTheirOriginalRow(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("work")
	input := key(s, "enter")[0].input
	s.Apply(Event{TurnID: input.TurnID, Kind: ToolStarted, ToolID: "one", ToolName: "Bash", Input: json.RawMessage(`{"command":"echo one"}`)})
	s.Apply(Event{TurnID: input.TurnID, Kind: TextDelta, Text: "assistant after tool start"})
	s.Apply(Event{TurnID: input.TurnID, Kind: ToolProgress, ToolID: "one", Text: "line 1"})
	s.Apply(Event{TurnID: input.TurnID, Kind: ToolFinished, ToolID: "one", Output: json.RawMessage(`{"stdout":"done"}`)})
	if len(s.Entries) != 3 || s.Entries[1].Running || s.Entries[1].Kind != "tool" || s.Entries[2].Kind != "assistant" {
		t.Fatalf("tool output reordered transcript: %#v", s.Entries)
	}
	if !strings.Contains(s.Entries[1].Text, "done") || strings.Contains(s.Entries[1].Text, `{"stdout":"done"}`) {
		t.Fatal("tool result missing")
	}
}

func TestCtrlExitUsesIndependentEightHundredMillisecondWindows(t *testing.T) {
	s := NewState(nil, 80, 24)
	now := time.Unix(100, 0)
	if len(s.HandleKey(Key{Name: "ctrl+c"}, now)) != 0 || s.Closing {
		t.Fatal("first Ctrl-C exited")
	}
	if len(s.HandleKey(Key{Name: "ctrl+d"}, now.Add(200*time.Millisecond))) != 0 || s.Closing {
		t.Fatal("different key counted as second press")
	}
	if len(s.HandleKey(Key{Name: "ctrl+c"}, now.Add(801*time.Millisecond))) != 0 || s.Closing {
		t.Fatal("expired Ctrl-C window exited")
	}
	if actions := s.HandleKey(Key{Name: "ctrl+c"}, now.Add(1601*time.Millisecond)); len(actions) != 1 || actions[0].kind != actionQuit || !s.Closing {
		t.Fatal("800ms boundary did not exit")
	}
}

func TestCtrlDDeletesInsteadOfExitingWithNonemptyInput(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("中文")
	s.Editor.Home()
	key(s, "ctrl+d")
	key(s, "ctrl+d")
	if s.Editor.Text() != "" || s.Closing {
		t.Fatal("Ctrl-D did not delete input graphemes")
	}
}

func TestHistorySearchCancelRestoresDraftAndExecuteUsesOriginalBytes(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.history = []HistoryEntry{{Display: "old 中文"}, {Display: "new emoji 👋"}}
	s.Editor.Set("draft")
	key(s, "ctrl+r")
	s.HandleKey(Key{Name: "中", Text: "中"}, time.Unix(100, 0))
	if s.Editor.Text() != "old 中文" {
		t.Fatal("history search failed")
	}
	key(s, "ctrl+c")
	if s.Editor.Text() != "draft" || s.Searching {
		t.Fatal("history cancellation lost draft")
	}
	key(s, "ctrl+r")
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].input.Text != "new emoji 👋" {
		t.Fatal("history execute changed message")
	}
}

func TestAutocompleteAndTranscriptHaveInputPriority(t *testing.T) {
	s := NewState([]Completion{{Name: "resume", Description: "resume a session"}, {Name: "review"}}, 80, 24)
	s.HandleKey(Key{Name: "/re", Text: "/re"}, time.Unix(100, 0))
	key(s, "down")
	if len(s.Completions) != 2 || s.Selected != 1 {
		t.Fatal("completion navigation lost selection")
	}
	key(s, "tab")
	if s.Editor.Text() != "/review " || s.Busy() {
		t.Fatal("completion performed a turn")
	}
	key(s, "ctrl+o")
	s.HandleKey(Key{Name: "x", Text: "x"}, time.Unix(100, 0))
	if s.Editor.Text() != "/review " {
		t.Fatal("transcript reader edited prompt")
	}
	key(s, "esc")
	if s.Transcript {
		t.Fatal("transcript did not exit")
	}
}

func TestResizeDoesNotAlterInputOrPendingPermission(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("中文👨‍👩‍👧‍👦\nsecond")
	before := s.Editor.Text()
	for _, dimensions := range [][2]int{{1, 1}, {2, 3}, {10, 4}, {80, 24}, {0, 0}} {
		s.Resize(dimensions[0], dimensions[1])
		view := s.View("Claude Code", true)
		if s.Editor.Text() != before {
			t.Fatal("resize changed prompt")
		}
		if strings.Count(view.Content, "\n")+1 > s.Height {
			t.Fatalf("render beyond terminal height %d: %q", s.Height, view.Content)
		}
		if view.Cursor != nil && (view.Cursor.X >= s.Width || view.Cursor.Y >= s.Height) {
			t.Fatalf("cursor outside terminal: %#v", view.Cursor)
		}
	}
}

func TestEOFIsIdempotentAndCancelsActiveTurnBeforeQuit(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("work")
	key(s, "enter")
	s.Editor.Set("queued")
	key(s, "enter")
	actions := s.EOF()
	if len(actions) != 2 || actions[0].kind != actionCancel || actions[1].kind != actionQuit || len(s.Queue) != 0 {
		t.Fatal("EOF did not cancel then close")
	}
	if len(s.EOF()) != 0 {
		t.Fatal("EOF closed twice")
	}
}

func TestRendererNeverExecutesToolOrModelTerminalEscapes(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Entries = []Entry{{Kind: "assistant", Text: "safe\x1b]52;c;malicious\a\x1b[2J\x1b[38;2;215;119;87m中文\x1b[39m"}}
	view := s.View("", false)
	// Only the trusted prompt's exact Source theme SGR pair is permitted.
	// Counting it before removal also catches a model injecting the same color.
	accent := TerminalThemeRoleEscape("dark", "claude", false)
	if strings.Count(view.Content, accent) != 1 || strings.Count(view.Content, "\x1b[39m") != 1 {
		t.Fatalf("untrusted theme SGR leaked: %q", view.Content)
	}
	visible := strings.ReplaceAll(strings.ReplaceAll(view.Content, accent, ""), "\x1b[39m", "")
	if strings.ContainsAny(visible, "\x1b\a") || !strings.Contains(visible, "safe中文") {
		t.Fatalf("terminal escapes leaked: %q", view.Content)
	}
}

func TestPermissionChoicePreservesEditedInputAndHostRule(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("work")
	input := key(s, "enter")[0].input
	updated := json.RawMessage(`{"command":"echo approved","unknown":{"kept":true}}`)
	rule := json.RawMessage(`{"type":"tool-rule","behavior":"allow","scope":"project","unknown":7}`)
	request := &PermissionRequest{ID: "permit-options", ToolName: "Bash", Choices: []PermissionChoice{
		{Key: "y", Label: "Allow once", Decision: PermissionDecision{Allowed: true}},
		{Key: "e", Label: "Allow changed command and save rule", Decision: PermissionDecision{Allowed: true, UpdatedInput: updated, Rule: rule}},
	}}
	s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: request})
	key(s, "down")
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].permission != request.ID || !actions[0].decision.Allowed || string(actions[0].decision.UpdatedInput) != string(updated) || string(actions[0].decision.Rule) != string(rule) {
		t.Fatalf("permission choice lost original decision: %#v", actions)
	}
}

func TestBashModeSurvivesHistoryAndStash(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.HandleKey(Key{Name: "!", Text: "!"}, time.Unix(100, 0))
	s.Editor.Set("echo 中文")
	first := key(s, "enter")[0].input
	if first.Mode != "bash" || first.Text != "echo 中文" {
		t.Fatal("bash submission changed")
	}
	s.FinishTurn(first.TurnID, nil)
	key(s, "up")
	if s.Mode != "bash" || s.Editor.Text() != "echo 中文" {
		t.Fatal("history lost bash mode")
	}
	key(s, "ctrl+s")
	if s.Mode != "prompt" || s.Editor.Text() != "" {
		t.Fatal("stash did not release prompt")
	}
	key(s, "ctrl+s")
	if s.Mode != "bash" || s.Editor.Text() != "echo 中文" {
		t.Fatal("stash lost bash mode")
	}
}

func TestPermissionHostChoicesCannotBeBypassedByDefaultAllowKey(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("work")
	input := key(s, "enter")[0].input
	s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: &PermissionRequest{ID: "restricted", Choices: []PermissionChoice{{Key: "n", Label: "Deny", Decision: PermissionDecision{Allowed: false}}}}})
	if actions := key(s, "y"); len(actions) != 0 || len(s.Permissions) != 1 {
		t.Fatal("default key invented an unavailable host decision")
	}
}

func TestPasteDeterminesBashModeAfterNormalizationAndNeverSubmits(t *testing.T) {
	for _, mode := range []string{"prompt", "bash"} {
		state := NewState(nil, 80, 24)
		state.Mode = mode
		state.Paste("\x1b[32m!echo 中文\x1b[0m\r\t😀")
		if state.Mode != "bash" || state.Editor.Text() != "echo 中文\n    😀" || state.Busy() {
			t.Fatalf("paste normalization/mode: %+v %q", state, state.Editor.Text())
		}
	}
	state := NewState(nil, 80, 24)
	state.Editor.Set("existing")
	state.Paste("!echo 中文")
	if state.Mode != "prompt" || state.Editor.Text() != "existing!echo 中文" {
		t.Fatal("non-empty prompt changed input mode")
	}
}
