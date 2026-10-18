package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSavedTeammateTranscriptLoaderAndWholeMessageReplay(t *testing.T) {
	message := presentationObject(mustLocalMessageJSON(map[string]any{"from": "A\"&<", "text": "saved full body with 汉字😀\nactual next saved line", "summary": "actual saved summary", "color": "blue"}))
	rows := []terminalTeammateMessageSourceRow{}
	for _, width := range []int{100, 47, 24} {
		for _, transcript := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				rows = append(rows, terminalTeammateMessageSourceRow{Width: width, Height: 80, Theme: "dark", Transcript: transcript, Preview: preview, Expanded: "none", Messages: []protocol.JSONObject{message}})
			}
		}
	}
	frames := terminalTeammateOriginalMessages(t, rows, true)
	for i, row := range rows {
		frame := frames[i]
		if len(frame.LoadedRecords) != 1 || !frame.TeamsEnabled {
			t.Fatalf("Original physical transcript loader/gate missing profile%d %v", i, frame)
		}
		before, _ := json.Marshal(frame.LoadedRecords)
		state := NewState(nil, row.Width, row.Height)
		state.Preferences = TerminalPreferences{Theme: row.Theme, ReducedMotion: true}
		state.taskTeamsEnabled = frame.TeamsEnabled
		state.ShowTeammatePreview, state.Transcript = row.Preview, row.Transcript
		state.SetInitialHistory(frame.LoadedRecords, false)
		if len(state.Entries) != 1 || state.Entries[0].Kind != "user" || state.Entries[0].Text != frame.Formatted {
			t.Fatalf("Saved canonical user XML must remain intact profile%d %v", i, state.Entries)
		}
		got := state.transcriptLines()
		if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, row.Width), terminalTaskListCells(got, row.Width)) {
			t.Errorf("Actual saved loader→State transcript profile%d width%d Source%q Go%q", i, row.Width, frame.Rows, got)
		}
		view := strings.Split(state.View("", false).Content, "\n")
		if len(view) < len(got) || !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, row.Width), terminalTaskListCells(view[:len(got)], row.Width)) {
			t.Errorf("Actual saved loader→State.View profile%d Source%q View%q", i, frame.Rows, view)
		}
		after, _ := json.Marshal(frame.LoadedRecords)
		if !bytes.Equal(before, after) {
			t.Fatal("History presentation changed original canonical transcript", i)
		}
	}
}

func TestActualSourceTeammateNotificationOriginWholeMessageCellsAndStyles(t *testing.T) {
	stamp, request := "2026-10-01T00:00:00.000Z", "actual-request"
	protocols := []map[string]any{
		{"type": "plan_approval_request", "from": "worker", "timestamp": stamp, "planFilePath": filepath.Join(t.TempDir(), "actual-plan.md"), "planContent": "## Actual plan\n\n- Inspect 汉字😀 file\n- Save result", "requestId": request},
		{"type": "plan_approval_response", "requestId": request, "approved": true, "timestamp": stamp},
		{"type": "plan_approval_response", "requestId": request, "approved": false, "feedback": "Revise actual 汉字😀 plan", "timestamp": stamp},
		{"type": "shutdown_request", "requestId": request, "from": "worker", "reason": "Actual shutdown reason", "timestamp": stamp},
		{"type": "shutdown_rejected", "requestId": request, "from": "worker", "reason": "Still writing actual file", "timestamp": stamp},
		{"type": "task_assignment", "taskId": "12", "subject": "Actual 汉字😀 task", "description": "Actual multiline description\nretain original owner", "assignedBy": "team-lead", "timestamp": stamp},
		{"type": "task_completed", "from": "worker", "taskId": "12", "taskSubject": "Actual 汉字😀 task"},
		{"type": "plan_approval_request", "requestId": request, "unexpected": "invalid missing required fields"},
	}
	rows := []terminalTeammateMessageSourceRow{}
	for _, body := range protocols {
		text := string(mustLocalMessageJSON(body))
		message := presentationObject(mustLocalMessageJSON(map[string]any{"from": "worker", "color": "blue", "summary": "actual protocol summary", "text": text}))
		for _, width := range []int{100, 24} {
			for _, attachment := range []bool{false, true} {
				for _, transcript := range []bool{false, true} {
					rows = append(rows, terminalTeammateMessageSourceRow{Width: width, Height: 80, Theme: "dark", Transcript: transcript, Attachment: attachment, Expanded: "none", Messages: []protocol.JSONObject{message}})
				}
			}
		}
	}
	frames := terminalTeammateOriginalMessages(t, rows)
	for i, row := range rows {
		t.Run(fmt.Sprintf("%d-%s-width%d-attachment%t-transcript%t", i, protocols[i/8]["type"], row.Width, row.Attachment, row.Transcript), func(t *testing.T) {
			state := NewState(nil, row.Width, row.Height)
			state.Preferences = TerminalPreferences{Theme: row.Theme, ReducedMotion: true}
			state.taskTeamsEnabled = frames[i].TeamsEnabled
			before, _ := json.Marshal(row.Messages)
			event := Event{Kind: TeammateMessages, Result: before}
			// An old candidate lacks the new ephemeral origin field. Its real
			// renderer must fail the same Source frames rather than fail build.
			field := reflect.ValueOf(&event).Elem().FieldByName("TeammateOrigin")
			if row.Attachment && field.IsValid() {
				field.SetString("teammate_mailbox")
			}
			state.Apply(event)
			state.Transcript = row.Transcript
			if len(state.Entries) != 1 || row.Attachment && field.IsValid() && state.Entries[0].Subtype != "teammate_mailbox" {
				t.Fatal("Actual event origin was not retained by its ephemeral Entry", state.Entries)
			}
			got := state.transcriptLines()
			if !reflect.DeepEqual(terminalTaskListCells(frames[i].PaintRows, row.Width), terminalTaskListCells(got, row.Width)) {
				t.Fatalf("Original whole Message origin branch Source%q Go%q", frames[i].Rows, got)
			}
			view := strings.Split(state.View("", false).Content, "\n")
			if len(view) < len(got) || !reflect.DeepEqual(terminalTaskListCells(frames[i].PaintRows, row.Width), terminalTaskListCells(view[:len(got)], row.Width)) {
				t.Fatal("Actual State.View lost original notification cells", frames[i].Rows, view)
			}
			after, _ := json.Marshal(row.Messages)
			wire, err := json.Marshal(event)
			entry, entryErr := json.Marshal(state.Entries[0])
			if !bytes.Equal(before, after) || err != nil || entryErr != nil || bytes.Contains(wire, []byte(`"TeammateOrigin"`)) || bytes.Contains(entry, []byte(`"Subtype"`)) {
				t.Fatal("Transient presentation origin altered canonical/SDK JSON", err, entryErr)
			}
		})
	}
}

func TestActualSourceLiveTeammateXMLGateAndMountedTranscriptPropChanges(t *testing.T) {
	message := presentationObject(mustLocalMessageJSON(map[string]any{"from": "worker", "text": "actual raw body with 汉字😀", "summary": "actual summary", "color": "blue"}))
	rows := []terminalTeammateMessageSourceRow{}
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{100, 47, 24} {
			for _, enabled := range []bool{false, true} {
				enabled := enabled
				rows = append(rows, terminalTeammateMessageSourceRow{Width: width, Height: 80, Theme: theme, Expanded: "none", Messages: []protocol.JSONObject{message}, TeamsEnabled: &enabled, GateSteps: []terminalTeammateGateStep{{false, false}, {false, true}, {true, false}, {false, true}}})
			}
		}
	}
	frames := terminalTeammateOriginalMessages(t, rows)
	for index, row := range rows {
		frame := frames[index]
		state := NewState(nil, row.Width, row.Height)
		state.Preferences = TerminalPreferences{Theme: row.Theme, ReducedMotion: true}
		setGate := func(enabled bool) {
			state.applyTerminalTaskList(mustLocalMessageJSON(map[string]any{"enabled": true, "teamsEnabled": enabled, "tasks": []any{}}))
		}
		setGate(frame.TeamsEnabled)
		state.Apply(Event{Kind: TeammateMessages, Text: frame.Formatted, Result: mustLocalMessageJSON(row.Messages)})
		if len(state.Entries) != 1 || state.Entries[0].Kind != "user" || state.Entries[0].Text != frame.Formatted {
			t.Fatalf("Actual live XML producer must retain the complete original user input profile%d %v", index, state.Entries)
		}
		check := func(want []string, step int) {
			got := state.transcriptLines()
			if !reflect.DeepEqual(terminalTaskListCells(want, row.Width), terminalTaskListCells(got, row.Width)) {
				t.Fatalf("Original live XML gate/cached Message profile%d step%d Source%q Go%q", index, step, want, got)
			}
			view := strings.Split(state.View("", false).Content, "\n")
			if len(view) < len(got) || !reflect.DeepEqual(terminalTaskListCells(want, row.Width), terminalTaskListCells(view[:len(got)], row.Width)) {
				t.Fatalf("Actual State.View lost original live XML cells profile%d step%d", index, step)
			}
		}
		check(frame.PaintRows, -1)
		if len(frame.GateFrames) != len(row.GateSteps) {
			t.Fatal("Original same-key mounted Message transitions missing", index)
		}
		for step, original := range frame.GateFrames {
			setGate(original.TeamsEnabled)
			if state.Transcript != original.Transcript {
				state.HandleKey(Key{Name: "ctrl+o"}, time.Now())
			}
			if state.Transcript != original.Transcript {
				t.Fatal("Actual transcript shortcut failed", index, step)
			}
			check(original.PaintRows, step)
			if state.Entries[0].Text != frame.Formatted {
				t.Fatal("Gate/paint changed canonical user XML", index, step)
			}
		}
	}
}
