package tui

import (
	"testing"
	"time"
)

func TestTerminalBtwImmediateBusyInputAndModalCloseDoNotCancelParent(t *testing.T) {
	s := NewState([]Completion{{Name: "btw", Kind: "local-jsx"}}, 100, 35)
	s.ActiveTurn = "actual-parent"
	s.Entries = []Entry{{TurnID: "actual-parent", Kind: "user", Text: "parent human"}}
	s.Editor.Set("/btw owned question")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != "command:panel:btw\x00owned question" || len(s.Queue) != 0 || s.ActiveTurn != "actual-parent" || len(s.Entries) != 1 {
		t.Fatal("Source immediate BTW was queued or became a parent query", actions, s)
	}
	for _, key := range []Key{{Name: "esc"}, {Name: "escape"}, {Name: "enter"}, {Name: "space"}, {Text: " "}, {Name: "ctrl+c"}, {Name: "ctrl+d"}} {
		s.openLocalPanel(&LocalPanel{ID: "owned-btw", Kind: "btw", Presentation: []byte(`{"question":"owned question"}`)})
		actions = s.HandleKey(key, time.Now())
		if len(actions) != 1 || actions[0].kind != actionLocalPanel || actions[0].panel.Action != "btw-close" || s.cancelling || s.Closing || s.ActiveTurn != "actual-parent" {
			t.Fatal("BTW close delegated parent cancellation or exit", key, actions)
		}
	}
	s.openLocalPanel(&LocalPanel{ID: "owned-btw", Kind: "btw", Presentation: []byte(`{"question":"owned question","response":"A\nB\nC\nD\nE\nF\nG"}`)})
	s.HandleKey(Key{Name: "down"}, time.Now())
	s.LocalPanel.terminalBtwLines(100, 16, "dark", time.Now())
	if s.LocalPanel.scroll != 2 {
		t.Fatal("Actual 3-line scroll with Source height clamp", s.LocalPanel.scroll)
	}
	s.Apply(Event{Kind: LocalPanelClosed})
	s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &LocalPanel{ID: "owned-btw", Kind: "btw", Presentation: []byte(`{"response":"late"}`)}})
	if s.LocalPanel != nil || s.ActiveTurn != "actual-parent" {
		t.Fatal("Queued old side response reopened closed panel or ended parent")
	}
}
