package tui

import (
	"strings"
	"testing"
	"time"
)

func TestSelectedRequiredPromptKeepsArgumentsEditableAndShowsProgressiveHints(t *testing.T) {
	s := NewState([]Completion{{Name: "deploy", Kind: "prompt", RequiresArguments: true, ArgumentHint: "<target> <region>", ArgumentNames: []string{"target", "region"}}}, 100, 30)
	s.Paste("/dep")
	if actions := s.HandleKey(Key{Name: "enter"}, time.Now()); len(actions) != 0 || s.Editor.Text() != "/deploy " || s.Busy() || s.ArgumentHint != "<target> <region>" {
		t.Fatal("Source named-argument choice submitted too early", actions, s.Editor.Text(), s.ArgumentHint)
	}
	s.Paste("\"hello world\" ")
	if s.ArgumentHint != "[region]" || !strings.Contains(s.View("", false).Content, "[region]") {
		t.Fatal("original parser counted quoted words separately", s.ArgumentHint)
	}
	s.Paste("us ")
	if s.ArgumentHint != "" {
		t.Fatal("fully supplied args retain stale hint", s.ArgumentHint)
	}
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].input.Text != "/deploy \"hello world\" us " || s.ArgumentHint != "" {
		t.Fatal("actual prompt arguments not submitted intact", actions)
	}
}

func TestCommandHintStaticOnlyFirstSpaceAndNoHintWhileTypingArgument(t *testing.T) {
	s := NewState([]Completion{{Name: "command", Kind: "prompt", ArgumentHint: "static hint", ArgumentNames: []string{"one", "two"}}}, 100, 30)
	for _, row := range []struct{ Input, Hint string }{{"/command ", "static hint"}, {"/command  ", "[one] [two]"}, {"/command value", ""}, {"/command value ", "[two]"}, {"/command 'two words' ", "[two]"}, {"/command $VALUE ", "[two]"}, {"/command * ", "[one] [two]"}, {"/missing ", ""}} {
		s.Editor.Set(row.Input)
		s.updateCompletions()
		if s.ArgumentHint != row.Hint || len(s.Completions) > 0 {
			t.Errorf("%q hint=%q expected=%q", row.Input, s.ArgumentHint, row.Hint)
		}
	}
	s.Mode = "bash"
	s.Editor.Set("/command ")
	s.updateCompletions()
	if s.ArgumentHint != "" {
		t.Fatal("bash shows prompt hint")
	}
}
