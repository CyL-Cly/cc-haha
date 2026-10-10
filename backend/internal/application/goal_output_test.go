package application

import (
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualGoalSDKOutputRemainsVisibleAcrossTurnSettlement(t *testing.T) {
	var events []tui.Event
	session := &Session{turnID: "goal-turn", emit: func(event tui.Event) error { events = append(events, event); return nil }}
	if err := session.output([]byte(`{"type":"system","subtype":"local_command_output","content":"<local-command-stdout>Goal set: real work</local-command-stdout>"}`)); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != tui.EventKind("local_command_output") || events[0].Text != "Goal set: real work" {
		t.Fatal(events)
	}
	state := tui.NewState(nil, 100, 30)
	state.Editor.Set("/goal real work")
	state.HandleKey(tui.Key{Name: "enter"}, time.Now())
	event := events[0]
	event.TurnID = state.ActiveTurn
	state.Apply(event)
	state.FinishTurn(state.ActiveTurn, nil)
	if !strings.Contains(state.View("", false).Content, "Goal set: real work") {
		t.Fatal("goal output disappeared at turn settlement")
	}
	if strings.Contains(events[0].Text, "<local-command") {
		t.Fatal("raw tags displayed")
	}
}
