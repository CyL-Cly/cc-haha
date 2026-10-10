package tui

import (
	"strings"
	"testing"
	"time"
)

func TestThinkingPickerTwoOptionsCurrentDefaultAndMidConversationConfirmation(t *testing.T) {
	for _, current := range []bool{false, true} {
		for _, mid := range []bool{false, true} {
			s := NewState(nil, 100, 30)
			s.Editor.Set("preserved draft")
			s.OpenThinkingPicker(ThinkingPicker{ID: "owned", Current: current, MidConversation: mid})
			now := time.Now()
			view := s.View("", true).Content
			label := "> Disabled"
			if current {
				label = "> Enabled"
			}
			if !strings.Contains(view, label) {
				t.Fatal("Source initial focus", view)
			}
			s.HandleKey(Key{Name: "down"}, now)
			actions := s.HandleKey(Key{Name: "enter"}, now)
			if mid {
				if len(actions) != 0 || s.ThinkingPicker.pending == nil || !strings.Contains(s.View("", true).Content, "Do you want to proceed?") {
					t.Fatal("Source mid-conversation confirmation missing")
				}
				s.HandleKey(Key{Name: "esc"}, now)
				if s.ThinkingPicker == nil || s.ThinkingPicker.pending != nil {
					t.Fatal("confirmation cancel did not return to options")
				}
				s.HandleKey(Key{Name: "enter"}, now)
				actions = s.HandleKey(Key{Name: "enter"}, now)
			}
			if len(actions) != 1 || actions[0].kind != actionThinkingPicker || actions[0].permission != "owned" || actions[0].thinkingPickerResult.Enabled == current || s.ThinkingPicker != nil || s.Editor.Text() != "preserved draft" {
				t.Fatal("original picker callback/draft", actions, s.Editor.Text())
			}
		}
	}
}
func TestThinkingPickerSameValueAvoidsWarningAndCancelDoesNotChangeValue(t *testing.T) {
	s := NewState(nil, 100, 30)
	s.OpenThinkingPicker(ThinkingPicker{ID: "owned", Current: true, MidConversation: true})
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || !actions[0].thinkingPickerResult.Enabled {
		t.Fatal("same value should apply directly", actions)
	}
	s.OpenThinkingPicker(ThinkingPicker{ID: "cancel", Current: false})
	actions = s.HandleKey(Key{Name: "esc"}, time.Now())
	if len(actions) != 1 || !actions[0].thinkingPickerResult.Cancelled {
		t.Fatal("cancel mutated Source value", actions)
	}
}

func TestThinkingPickerHonorsOriginalCustomBindingsAndPreservesPaste(t *testing.T) {
	s := NewState(nil, 100, 30)
	s.Editor.Set("draft before modal")
	next, accept, cancel := "select:next", "select:accept", "confirm:no"
	s.SetKeybindings([]Keybinding{{Context: "Select", Chord: ParseChord("x"), Action: &next}, {Context: "Select", Chord: ParseChord("a"), Action: &accept}, {Context: "Confirmation", Chord: ParseChord("z"), Action: &cancel}})
	s.OpenThinkingPicker(ThinkingPicker{ID: "custom", Current: true, MidConversation: true})
	s.Paste("ignored while modal")
	if actions := s.HandleKey(Key{Name: "x", Text: "x"}, time.Now()); len(actions) != 0 || s.ThinkingPicker.selected != 1 {
		t.Fatal("remapped selection ignored")
	}
	s.HandleKey(Key{Name: "a", Text: "a"}, time.Now())
	if s.ThinkingPicker.pending == nil {
		t.Fatal("remapped acceptance skipped confirmation")
	}
	s.HandleKey(Key{Name: "z", Text: "z"}, time.Now())
	if s.ThinkingPicker == nil || s.ThinkingPicker.pending != nil {
		t.Fatal("original remapped confirmation cancel")
	}
	actions := s.HandleKey(Key{Name: "z", Text: "z"}, time.Now())
	if len(actions) != 1 || !actions[0].thinkingPickerResult.Cancelled || s.Editor.Text() != "draft before modal" {
		t.Fatal("modal changed underlying input", actions, s.Editor.Text())
	}
}

func TestThinkingNotificationExpiresWithoutClearingActualTurnStatus(t *testing.T) {
	s := NewState(nil, 100, 30)
	s.Status = "Working…"
	s.applyNotice(Event{Kind: Notice, Text: "Thinking on", NoticeTimeout: 3 * time.Second})
	deadline := s.notificationUntil
	s.Tick(deadline.Add(-time.Nanosecond))
	if s.Notification != "Thinking on" || !strings.Contains(s.View("", false).Content, "Thinking on") {
		t.Fatal("Source 3000ms notification expired early")
	}
	s.Tick(deadline)
	if s.Notification != "" || s.Status != "Working…" {
		t.Fatal("notification hid ongoing task status", s.Status)
	}
	// A subsequent toggle replaces the keyed notification, extending its own
	// lifetime without retaining the old on/off text.
	s.applyNotice(Event{Kind: Notice, Text: "Thinking off", NoticeTimeout: 3 * time.Second})
	if s.Notification != "Thinking off" || !s.notificationUntil.After(deadline.Add(-time.Second)) {
		t.Fatal("replacement notification missing")
	}
}
