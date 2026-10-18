package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestActualTaskListWatcherFrameKeepsParentCollapsedAcrossNextHuman(t *testing.T) {
	// The actual native shared-list subscriber omits expandedView until a real
	// setter transition. The original mounted REPL receives this pending task
	// after the teammate's async direct setter no-op, with no task region paint.
	for _, viewField := range []string{"", `,"expandedView":"none","expandedViewRevision":1`} {
		t.Run(viewField, func(t *testing.T) {
			s := NewState(nil, 100, 36)
			m := &terminalModel{ctx: context.Background(), state: s, expandedView: "none"}
			raw := json.RawMessage(`{"tasks":[{"id":"1","subject":"ACTUAL_TEAMMATE_SHARED_TASK","description":"Original child write reaches mounted parent store","status":"pending","blocks":[],"blockedBy":[]}],"listId":"task-source-team","teamsEnabled":true` + viewField + `}`)
			m.Update(terminalTaskListChangedMsg{raw})
			if len(s.TaskItems) != 1 || s.terminalExpandedView() != "none" || strings.Contains(s.View("", false).Content, "ACTUAL_TEAMMATE_SHARED_TASK") {
				t.Fatal("A real list publication silently expanded the parent", s.terminalExpandedView(), s.View("", false).Content)
			}
			s.HandleKey(Key{Text: "ACTUAL_PARENT_NEXT_HUMAN"}, time.Now())
			act := s.HandleKey(Key{Name: "enter"}, time.Now())
			// Original Spinner shows its Next hint during loading even with the
			// TaskList collapsed. The mounted Source counterpart is included in
			// TestActualMountedSourceTaskListREPLSpinnerModalSixThemeCellsAndStyles.
			if len(act) != 1 || act[0].input.Text != "ACTUAL_PARENT_NEXT_HUMAN" || s.terminalExpandedView() != "none" || len(s.expandedTaskLines()) != 0 || !strings.Contains(s.View("", false).Content, "Next: ACTUAL_TEAMMATE_SHARED_TASK") {
				t.Fatal("Next real parent submission changed the collapsed task view", act, s.View("", false).Content)
			}
			s.FinishTurn(s.ActiveTurn, nil)
			if s.terminalExpandedView() != "none" || len(s.expandedTaskLines()) != 0 || strings.Contains(s.View("", false).Content, "ACTUAL_TEAMMATE_SHARED_TASK") {
				t.Fatal("Completed real parent turn retained the Spinner hint or expanded the task view", s.View("", false).Content)
			}
			s.HandleKey(Key{Name: "ctrl+t"}, time.Now())
			if s.terminalExpandedView() != "tasks" || len(s.expandedTaskLines()) == 0 || !strings.Contains(s.View("", false).Content, "ACTUAL_TEAMMATE_SHARED_TASK") {
				t.Fatal("The explicit human task-view toggle lost the shared task", s.View("", false).Content)
			}
		})
	}
}
