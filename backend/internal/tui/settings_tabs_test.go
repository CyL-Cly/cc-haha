package tui

import (
	"testing"
	"time"
)

func TestUsageHeaderRetryConsumesRegisteredErrorActionAndCustomChord(t *testing.T) {
	s := NewState(nil, 80, 24)
	open := func(retry bool) {
		panel := LocalPanel{ID: "owned-usage", Kind: "usage", SettingsTabs: []string{"status", "config", "usage"}, SettingsHeaderFocused: true}
		if retry {
			panel.Actions = []LocalPanelAction{{Key: "r", Name: "usage-retry"}}
		}
		s.openLocalPanel(&panel)
	}
	assertRetry := func(key Key) {
		t.Helper()
		actions := s.HandleKey(key, time.Now())
		if len(actions) != 1 || actions[0].kind != actionLocalPanel || actions[0].panel.ID != "owned-usage" || actions[0].panel.Action != "usage-retry" {
			t.Fatal("header swallowed the actual error retry consumer", actions)
		}
	}
	open(true)
	assertRetry(Key{Name: "r", Text: "r"})
	name := "settings:retry"
	s.bindings = append(s.bindings, Keybinding{Context: "Settings", Action: &name, Chord: []Keystroke{{Key: "k", Ctrl: true}, {Key: "u", Ctrl: true}}})
	if actions := s.HandleKey(Key{Name: "ctrl+k"}, time.Now()); len(actions) != 0 || len(s.pendingChord) != 1 {
		t.Fatal("custom retry chord did not remain in the actual settings context", actions, s.pendingChord)
	}
	assertRetry(Key{Name: "ctrl+u"})
	open(false)
	if actions := s.HandleKey(Key{Name: "r", Text: "r"}, time.Now()); len(actions) != 0 {
		t.Fatal("loading/success Usage mounted an inactive retry callback", actions)
	}
	if actions := s.HandleKey(Key{Name: "ctrl+k"}, time.Now()); len(actions) != 0 {
		t.Fatal(actions)
	}
	if actions := s.HandleKey(Key{Name: "ctrl+u"}, time.Now()); len(actions) != 0 {
		t.Fatal("custom chord bypassed the actual error registration gate", actions)
	}
}
