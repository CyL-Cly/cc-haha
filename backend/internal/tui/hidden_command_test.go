package tui

import "testing"

func TestHiddenCommandDoesNotAppearForPrefixOrEmptyInput(t *testing.T) {
	s := NewState([]Completion{{Name: "heapdump", Kind: "local", Hidden: true}, {Name: "help", Kind: "local"}}, 100, 30)
	for _, input := range []string{"/", "/he", "/heap"} {
		s.Editor.Set(input)
		s.updateCompletions()
		for _, item := range s.Completions {
			if item.Name == "heapdump" {
				t.Fatal("original hidden command surfaced for prefix", input)
			}
		}
	}
	s.Editor.Set("/heapdump")
	s.updateCompletions()
	if s.Editor.Text() != "/heapdump" {
		t.Fatal("hidden exact command cannot be typed")
	}
}
