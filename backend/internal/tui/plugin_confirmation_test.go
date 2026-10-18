package tui

import (
	"testing"
	"time"
)

func TestPluginProjectConfirmationUsesOnlyConfirmationBindingsAndCleanupRawKeys(t *testing.T) {
	s := NewState(nil, 100, 35)
	now := time.Now()
	panel := LocalPanel{ID: "confirmation", Kind: "plugin", ConfirmationOnly: true, DismissAction: "plugin-back", Actions: []LocalPanelAction{{Key: "confirm:yes", Name: "plugin-disable-local"}, {Key: "confirm:no", Name: "plugin-back"}}}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
	for _, key := range []Key{{Name: "y", Text: "y"}, {Name: "enter"}, {Name: "n", Text: "n"}, {Name: "esc"}} {
		actions := s.HandleKey(key, now)
		want := "plugin-disable-local"
		if key.Name == "n" || key.Name == "esc" {
			want = "plugin-back"
		}
		if len(actions) != 1 || actions[0].kind != actionLocalPanel || actions[0].panel.Action != want {
			t.Fatal(key, actions)
		}
	}
	for _, key := range []Key{{Name: "up"}, {Name: "down"}, {Name: "j", Text: "j"}, {Name: "k", Text: "k"}, {Name: "space", Text: " "}} {
		if actions := s.HandleKey(key, now); len(actions) > 0 {
			t.Fatal("Select was mounted in Source confirmation", key, actions)
		}
	}
	s.SetKeybindings([]Keybinding{binding("Confirmation", "ctrl+k y", "confirm:yes"), binding("Confirmation", "alt+n", "confirm:no"), binding("Select", "y", "select:accept")})
	if actions := s.HandleKey(Key{Name: "y", Text: "y"}, now); len(actions) > 0 {
		t.Fatal("Select accept leaked into Confirmation", actions)
	}
	s.HandleKey(Key{Name: "ctrl+k"}, now)
	if actions := s.HandleKey(Key{Name: "y", Text: "y"}, now); len(actions) != 1 || actions[0].panel.Action != "plugin-disable-local" {
		t.Fatal(actions)
	}
	s.SetKeybindings(DefaultKeybindings("macos", true, KeybindingFeatures{}))
	panel.ConfirmationOnly = false
	panel.RawInputKeys = []string{"y", "n", "Y", "N", "enter"}
	panel.Actions = []LocalPanelAction{{Key: "y", Name: "delete"}, {Key: "Y", Name: "delete"}, {Key: "n", Name: "keep"}, {Key: "N", Name: "keep"}}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
	if actions := s.HandleKey(Key{Name: "enter"}, now); len(actions) > 0 {
		t.Fatal("Enter must not delete persistent data", actions)
	}
	for _, key := range []string{"y", "Y", "n", "N"} {
		actions := s.HandleKey(Key{Name: key, Text: key}, now)
		want := "delete"
		if key == "n" || key == "N" {
			want = "keep"
		}
		if len(actions) != 1 || actions[0].panel.Action != want {
			t.Fatal(key, actions)
		}
	}
	if actions := s.HandleKey(Key{Name: "esc"}, now); len(actions) != 1 || actions[0].panel.Action != "plugin-back" {
		t.Fatal(actions)
	}
}
