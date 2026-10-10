package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPermissionModeFooterMirrorsActualSessionAndUsesBoundShortcut(t *testing.T) {
	s := NewState(nil, 100, 30)
	s.Apply(Event{Kind: PermissionModeChanged, PermissionMode: "acceptEdits"})
	if !strings.Contains(s.View("", false).Content, "accept edits on") {
		t.Fatal("actual mode not visible")
	}
	cycle := "chat:cycleMode"
	s.SetKeybindings([]Keybinding{{Context: "Chat", Chord: ParseChord("ctrl+j"), Action: &cycle}})
	if view := s.View("", false).Content; !strings.Contains(view, "ctrl+j to cycle") || strings.Contains(view, "Shift+Tab") {
		t.Fatal("footer falsely advertises old shortcut", view)
	}
	actions := s.HandleKey(Key{Name: "ctrl+j"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != cycle {
		t.Fatal("same bound key not dispatched", actions)
	}
	s.Apply(Event{Kind: PermissionModeChanged, PermissionMode: "default"})
	if strings.Contains(s.View("", false).Content, "edits on") {
		t.Fatal("stale mode after owner restore")
	}
}
