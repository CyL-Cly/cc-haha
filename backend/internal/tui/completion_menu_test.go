package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCommandMenuShowsWindowTracksKeyboardAndRefreshes(t *testing.T) {
	commands := []Completion{}
	for i := 0; i < 12; i++ {
		commands = append(commands, Completion{Name: fmt.Sprintf("item%02d", i), Description: fmt.Sprintf("Actual shared command %d", i)})
	}
	s := NewState(commands, 100, 30)
	s.Paste("/item")
	now := time.Now()
	if lines := s.completionLines(true); len(lines) != 5 || !strings.Contains(lines[0], "> /item00") {
		t.Fatal("first Source window", lines)
	}
	for i := 0; i < 7; i++ {
		s.HandleKey(Key{Name: "down"}, now)
	}
	view := s.View("", true).Content
	if !strings.Contains(view, "> /item07") || strings.Contains(view, "/item00") {
		t.Fatal("selection is not visible", view)
	}
	s.HandleKey(Key{Name: "tab"}, now)
	if s.Editor.Text() != "/item07 " || len(s.Completions) != 0 {
		t.Fatal("accepted different command", s.Editor.Text())
	}
	s.Editor.Set("")
	s.Paste("/item")
	s.Apply(Event{Kind: CommandsChanged, Commands: []Completion{{Name: "item-real", Description: "fresh producer"}}})
	if view := s.View("", true).Content; !strings.Contains(view, "item-real") || strings.Contains(view, "item00") {
		t.Fatal("stale menu", view)
	}
	s.HandleKey(Key{Name: "esc"}, now)
	if len(s.Completions) != 0 || s.Editor.Text() != "/item" {
		t.Fatal("dismiss changed draft")
	}
}
