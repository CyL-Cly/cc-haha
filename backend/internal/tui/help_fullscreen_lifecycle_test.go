package tui

import (
	"strings"
	"testing"
)

func TestFullscreenHelpQueuedSubmitMountRetainsOnlyActualConversationMessages(t *testing.T) {
	s := NewState([]Completion{{Name: "help", Kind: "local-jsx"}}, 100, 36)
	s.fullscreen = true
	s.Entries = []Entry{{TurnID: "old", Kind: "user", Text: "original actual human"}}
	input := Input{TurnID: "queued-help", Mode: "prompt", Text: "/help actual args"}
	if actions := s.start(input); len(actions) != 1 || actions[0].input.TurnID != input.TurnID || actions[0].input.Text != input.Text {
		t.Fatal(actions)
	}
	s.Apply(Event{Kind: LocalPanelOpened, TurnID: input.TurnID, LocalPanel: &LocalPanel{ID: "actual-help-mount", Kind: "help", HelpDialog: true}})
	if !s.Busy() || s.ActiveTurn != input.TurnID {
		t.Fatal("actual onDone reservation released before completion", s.ActiveTurn)
	}
	for _, entry := range s.Entries {
		if entry.TurnID == input.TurnID && entry.Kind == "user" {
			t.Fatal("original processSlash appends zero fullscreen dismissal records; provisional command must not become conversation history", entry)
		}
	}
	if len(s.Entries) != 1 || s.Entries[0].Text != "original actual human" {
		t.Fatal("unrelated canonical conversation changed", s.Entries)
	}
	s.Apply(Event{Kind: LocalPanelClosed, LocalPanel: &LocalPanel{ID: "actual-help-mount", Kind: "help"}})
	s.FinishTurn(input.TurnID, nil)
	if strings.Contains(s.View("", true).Content, "You: /help") {
		t.Fatal("fullscreen dismissal left a provisional transcript record")
	}
}
