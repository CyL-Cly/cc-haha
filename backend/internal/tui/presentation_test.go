package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToolPresentationDecodesOutputAndNeverShowsJSONEscapes(t *testing.T) {
	state := NewState(nil, 90, 30)
	state.Paste("run real tools")
	actions := state.HandleKey(Key{Name: "enter"}, time.Now())
	id := actions[0].input.TurnID
	state.Apply(Event{TurnID: id, Kind: ToolStarted, ToolID: "bash-one", ToolName: "Bash", Input: json.RawMessage(`{"command":"printf 'first\\nsecond'","description":"actual command"}`)})
	state.Apply(Event{TurnID: id, Kind: ToolFinished, ToolID: "bash-one", Output: json.RawMessage(`"first\nsecond 中文"`)})
	view := state.View("", false).Content
	if !strings.Contains(view, "first\nsecond 中文") || strings.Contains(view, `"first\nsecond`) {
		t.Fatalf("tool transport JSON leaked: %s", view)
	}
}

func TestReadTypedResultHidesModelReminderPreservingOriginalBytes(t *testing.T) {
	state := NewState(nil, 90, 30)
	state.ActiveTurn = "real-read"
	input := json.RawMessage(`{"file_path":"/private/fixture/中文.txt"}`)
	output := json.RawMessage(`"     1→private file\n<system-reminder>MODEL_CONTEXT_ONLY_Z8</system-reminder>"`)
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolStarted, ToolID: "read-one", ToolName: "Read", Input: input})
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolFinished, ToolID: "read-one", Output: output, Result: json.RawMessage(`{"type":"text","file":{"filePath":"/private/fixture/中文.txt","content":"private file","numLines":1,"startLine":1,"totalLines":1}}`)})
	view := state.View("", false).Content
	if !strings.Contains(view, "Read 1 line") || strings.Contains(view, "MODEL_CONTEXT_ONLY_Z8") || strings.Contains(view, `\u003c`) {
		t.Fatal("tool rendering leaked model data", view)
	}
	if string(state.Entries[0].Input) != string(input) || string(state.Entries[0].Output) != string(output) {
		t.Fatal("presentation changed original transport bytes")
	}
}

func TestToolErrorPresentationKeepsOriginalRowAndErrorStatus(t *testing.T) {
	state := NewState(nil, 90, 30)
	state.ActiveTurn = "real-error"
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolStarted, ToolID: "fail-one", ToolName: "Bash", Input: json.RawMessage(`{"command":"exit 7"}`)})
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolFinished, ToolID: "fail-one", IsError: true, Output: json.RawMessage(`"Exit code 7\nactual stderr 中文"`)})
	view := state.View("", false).Content
	if !strings.Contains(view, "✗ Bash:") || !strings.Contains(view, "Error: Exit code 7\nactual stderr 中文") || state.Entries[0].Running {
		t.Fatal("original tool failure lost", view)
	}
}

func TestLongToolErrorIsSummarizedAndTranscriptExpandsOriginal(t *testing.T) {
	state := NewState(nil, 100, 80)
	state.ActiveTurn = "long-error"
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolStarted, ToolID: "failed", ToolName: "Bash", Input: json.RawMessage(`{"command":"actual command"}`)})
	message := "<tool_use_error><sandbox_violations>not human output</sandbox_violations><error>"
	for i := 0; i < 13; i++ {
		message += fmt.Sprintf("failure line %02d\n", i)
	}
	message += "</error></tool_use_error>"
	encoded, _ := json.Marshal(message)
	state.Apply(Event{TurnID: state.ActiveTurn, Kind: ToolFinished, ToolID: "failed", IsError: true, Output: encoded})
	normal := state.View("", false).Content
	if strings.Contains(normal, "not human output") || strings.Contains(normal, "failure line 12") || !strings.Contains(normal, "… +3 lines (ctrl+o to see all)") {
		t.Fatal("long error is not summarized", normal)
	}
	state.Transcript = true
	expanded := state.View("", false).Content
	if !strings.Contains(expanded, "failure line 12") || strings.Contains(expanded, "to see all") || strings.Contains(expanded, "not human output") {
		t.Fatal("transcript cannot inspect complete tool error", expanded)
	}
	if string(state.Entries[0].Output) != string(encoded) {
		t.Fatal("rendering rewrote actual harness result")
	}
}
