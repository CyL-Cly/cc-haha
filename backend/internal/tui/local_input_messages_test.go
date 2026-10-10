package tui

import (
	"testing"
	"time"
)

func TestActualLocalProcessedUsersReplaceDraftWithoutChangingTurnLifetime(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("/agent general-purpose")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionSubmit {
		t.Fatal("missing actual submission", actions)
	}
	id := s.ActiveTurn
	texts := []string{"Usage: /agent <agent> <prompt>"}
	s.Apply(Event{Kind: LocalUserMessages, TurnID: id, UserMessages: texts})
	texts[0] = "foreign mutation"
	if !s.Busy() || len(s.Entries) != 1 || s.Entries[0].Kind != "user" || s.Entries[0].Text != "Usage: /agent <agent> <prompt>" {
		t.Fatal("actual local message hidden or treated as assistant/turn result", s.Entries)
	}
	s.Apply(Event{Kind: LocalUserMessages, TurnID: "foreign", UserMessages: []string{"stale"}})
	if len(s.Entries) != 1 {
		t.Fatal("stale callback displayed")
	}
	s.FinishTurn(id, nil)
	s.Editor.Set("/modelonly")
	s.HandleKey(Key{Name: "enter"}, time.Now())
	s.Apply(Event{Kind: LocalUserMessages, TurnID: s.ActiveTurn, UserMessages: []string{"/modelonly", "This skill can only be invoked by Claude, not directly by users."}})
	if len(s.Entries) != 3 || s.Entries[1].Text != "/modelonly" || s.Entries[2].Text != "This skill can only be invoked by Claude, not directly by users." {
		t.Fatal("Source no-query visible users changed", s.Entries)
	}
}
