package tui

import (
	"strings"
	"testing"
	"time"
)

func TestTeammateInboxPendingFooterProcessedAndViewedTranscriptIsolation(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Apply(Event{Kind: TeammateInboxChanged, Result: []byte(`[{"id":"pending","from":"worker","text":"full private message","summary":"actual peer summary","status":"pending"},{"id":"processed","from":"other","text":"already delivered","status":"processed"}]`)})
	if view := s.View("", false).Content; !strings.Contains(view, "@worker · actual peer summary") || strings.Contains(view, "already delivered") {
		t.Fatal("inbox footer", view)
	}
	s.SetTeammateTasks(teammateTestTasks())
	for _, key := range []Key{{Name: "shift+down"}, {Name: "shift+down"}, {Name: "enter"}} {
		s.HandleKey(key, time.Now())
	}
	if view := s.View("", false).Content; strings.Contains(view, "actual peer summary") {
		t.Fatal("leader inbox leaked into teammate view", view)
	}
	s.Apply(Event{Kind: TeammateInboxChanged, Result: []byte(`[{"from":"worker","text":"full private message","status":"processed"}]`)})
	if len(s.teammateInboxLines()) != 0 {
		t.Fatal("processed messages remained pending")
	}
	s.Apply(Event{Kind: TeammateMessages, Result: []byte(`[{"from":"worker","text":"full delivered text","summary":"delivered summary"},{"from":"hidden","text":"{\"type\":\"idle_notification\"}"}]`)})
	s.Teammates.Exit()
	if view := s.View("", false).Content; !strings.Contains(view, "@worker› delivered summary") || strings.Contains(view, "full delivered text") || strings.Contains(view, "@hidden") {
		t.Fatal("Source collapsed teammate presentation", view)
	}
	s.Transcript = true
	if view := s.View("", false).Content; !strings.Contains(view, "full delivered text") {
		t.Fatal("teammate transcript omitted body", view)
	}
}

func TestAutonomousTeammateTurnUsesTerminalBusyCancelAndQueuedHumanLifecycle(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Apply(Event{Kind: TeammateTurnStarted, TurnID: "peer-turn", Background: true})
	if !s.Busy() {
		t.Fatal("actual autonomous turn did not set busy")
	}
	s.Editor.Set("next human")
	if actions := s.HandleKey(Key{Name: "enter"}, time.Now()); len(actions) != 0 || len(s.Queue) != 1 {
		t.Fatal("human started during autonomous turn", actions)
	}
	s.Apply(Event{Kind: TextDelta, TurnID: "peer-turn", Background: true, Text: "actual peer result"})
	if !strings.Contains(s.View("", false).Content, "actual peer result") {
		t.Fatal("autonomous model text not displayed")
	}
	actions := s.FinishTeammateTurn(Event{Kind: TeammateTurnFinished, TurnID: "peer-turn", Background: true})
	if len(actions) != 1 || actions[0].kind != actionSubmit || actions[0].input.Text != "next human" {
		t.Fatal("queued human lost at autonomous finish", actions)
	}
	s.FinishTurn(s.ActiveTurn, nil)
	s.Apply(Event{Kind: TeammateTurnStarted, TurnID: "second-peer", Background: true})
	if actions := s.HandleKey(Key{Name: "escape"}, time.Now()); len(actions) != 1 || actions[0].kind != actionCancel {
		t.Fatal("autonomous turn could not be interrupted", actions)
	}
	s.FinishTeammateTurn(Event{Kind: TeammateTurnFinished, TurnID: "second-peer", Background: true})
	if s.Busy() {
		t.Fatal("finished autonomous cancellation left terminal busy")
	}
}
