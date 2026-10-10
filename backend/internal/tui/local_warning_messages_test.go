package tui

import (
	"strings"
	"testing"
	"time"
)

func TestActualLocalBlockingWarningReplacesStagedPromptAndRemainsVisibleAfterSettlement(t *testing.T) {
	s := NewState(nil, 110, 30)
	s.Editor.Set("original staged prompt")
	s.HandleKey(Key{Name: "enter"}, time.Now())
	id := s.ActiveTurn
	values := []LocalProcessedMessage{{Kind: "system", Text: "UserPromptSubmit operation blocked by hook:\nphysical warning", Level: "warning"}}
	s.Apply(Event{Kind: LocalProcessedMessages, TurnID: "foreign", LocalMessages: values})
	if len(s.Entries) != 1 || s.Entries[0].Kind != "user" {
		t.Fatal("stale warning changed active turn")
	}
	s.Apply(Event{Kind: LocalProcessedMessages, TurnID: id, LocalMessages: values})
	values[0].Text = "foreign mutation"
	if !s.Busy() || len(s.Entries) != 1 || s.Entries[0].Kind != "system" || s.Entries[0].Level != "warning" || s.Entries[0].Text == "foreign mutation" {
		t.Fatal("warning projection changed identity/lifetime", s.Entries)
	}
	s.FinishTurn(id, nil)
	visible := strings.Join(s.transcriptLines(), "\n")
	if strings.Contains(visible, "You: original staged prompt") || !strings.Contains(visible, "● UserPromptSubmit operation blocked by hook:") || !strings.Contains(visible, "physical warning") {
		t.Fatal("Source warning missing or erased prompt still staged", visible)
	}
}
