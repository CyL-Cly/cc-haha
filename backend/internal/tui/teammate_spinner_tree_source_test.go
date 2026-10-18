package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// These are complete original SpinnerWithVerb/TeammateSpinnerTree frames.
// The rows retain completed tasks, real teammate identities and previews;
// the mounted consumer decides what is running and what each row displays.
func TestActualMountedSourceSpinnerTeammateTreeWholeFramesAndStyles(t *testing.T) {
	started := time.Now().Add(-40000 * time.Millisecond).UnixMilli()
	makeBackground := func(allIdle bool) map[string]any {
		result := map[string]any{}
		for _, member := range []struct {
			id, name, color, status string
			idle                    bool
		}{
			{"alpha", "Alpha", "blue", "running", allIdle},
			{"beta", "Beta", "red", "running", true},
			{"completed", "Completed", "green", "completed", false},
		} {
			result[member.id] = map[string]any{"id": member.id, "type": "in_process_teammate", "status": member.status, "isIdle": member.idle, "identity": map[string]any{"agentName": member.name, "agentId": member.name + "@owned", "teamName": "owned", "color": member.color}, "startTime": started, "totalPausedMs": 2500, "spinnerVerb": "Reading", "pastTenseVerb": "Worked", "progress": map[string]any{"tokenCount": 1234, "toolUseCount": 1, "recentActivities": []any{map[string]any{"isRead": true}, map[string]any{"isSearch": true}, map[string]any{"isRead": true}}}, "messages": []any{map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "older line\nlatest 中文😀 line"}, map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"description": "Actual tool description\nnext"}}}}}}}
		}
		return result
	}
	makeRow := func(theme string, width int, loading, allIdle bool, state map[string]any) map[string]any {
		return map[string]any{"tasks": []any{}, "background": makeBackground(allIdle), "expanded": "teammates", "theme": theme, "width": width, "height": 36, "loading": loading, "message": "Leader work", "responseLength": 4000, "elapsedMs": 31500, "pauseElapsedMs": 31500, "global": map[string]any{"btwUseCount": 1}, "spinnerState": state}
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 59, 24} {
			for _, loading := range []bool{true, false} {
				rows = append(rows, makeRow(theme, width, loading, false, map[string]any{}))
			}
		}
	}
	for _, state := range []map[string]any{
		{"viewSelectionMode": "selecting-agent", "selectedIPAgentIndex": -1},
		{"viewSelectionMode": "selecting-agent", "selectedIPAgentIndex": 0},
		{"viewSelectionMode": "selecting-agent", "selectedIPAgentIndex": 2},
		{"viewSelectionMode": "viewing-agent", "viewingAgentTaskId": "alpha"},
		{"viewSelectionMode": "viewing-agent", "viewingAgentTaskId": "beta"},
		{"showTeammateMessagePreview": true},
	} {
		rows = append(rows, makeRow("dark", 100, true, false, state))
	}
	for _, field := range []string{"shutdownRequested", "awaitingPlanApproval"} {
		row := makeRow("dark", 100, true, false, map[string]any{})
		row["background"].(map[string]any)["alpha"].(map[string]any)[field] = true
		rows = append(rows, row)
	}
	rows = append(rows, makeRow("dark", 100, false, true, map[string]any{}))
	frames := terminalSpinnerOriginalFrames(t, true, rows)
	for index, row := range rows {
		frame := frames[index][0]
		s := NewState(nil, row["width"].(int), 36)
		s.Preferences = TerminalPreferences{Theme: row["theme"].(string), ReducedMotion: true}
		if row["loading"].(bool) {
			s.ActiveTurn, s.Status = "team-query", "Leader work…"
		}
		background, err := json.Marshal(row["background"])
		if err != nil {
			t.Fatal(err)
		}
		s.SetTeammateTasks(presentationObject(background))
		s.Teammates.ExpandedView = "teammates"
		state := row["spinnerState"].(map[string]any)
		if value, ok := state["selectedIPAgentIndex"].(int); ok {
			s.Teammates.SelectedIPAgentIndex = value
		}
		if value, ok := state["viewSelectionMode"].(string); ok {
			s.Teammates.ViewSelectionMode = value
		}
		if value, ok := state["viewingAgentTaskId"].(string); ok {
			s.Teammates.ViewingAgentTaskID = value
		}
		if value, ok := state["showTeammateMessagePreview"].(bool); ok {
			s.ShowTeammatePreview = value
		}
		s.dreamClock = time.UnixMilli(frame.Now)
		clock := s.ensureTerminalSpinnerClock()
		clock.verb, clock.verbKnown, clock.responseUnits = "Leader work", true, 4000
		clock.started, clock.pausedAt = s.dreamClock.Add(-31500*time.Millisecond), s.dreamClock
		clock.loading = row["loading"].(bool)
		view := strings.Split(s.View("", false).Content, "\n")
		start := -1
		first := ""
		for _, line := range frame.Rows {
			if strings.TrimSpace(line) != "" {
				first = line
				break
			}
		}
		for i, line := range view {
			if strings.TrimRight(ansi.Strip(line), " ") == first {
				start = i
				if i > 0 && strings.TrimSpace(ansi.Strip(view[i-1])) == "" {
					start--
				}
				break
			}
		}
		got := []string{}
		if start >= 0 {
			got = view[start:min(len(view), start+len(frame.PaintRows))]
		}
		if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, s.Width), terminalTaskListCells(got, s.Width)) {
			t.Errorf("Original teammate-tree profile%d width%d theme%s loading%v state%v: Source %q Go %q wholeView %q", index, s.Width, s.Preferences.Theme, row["loading"], state, frame.Rows, got, view)
		}
	}
}
