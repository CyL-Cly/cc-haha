package tui

import (
	"strings"
	"testing"
	"time"
)

func TestDoctorPanelDismissAndRetainedInputWithoutSubmitting(t *testing.T) {
	for _, key := range []string{"enter", "esc"} {
		state := NewState(nil, 80, 24)
		state.Editor.Set("retained typed draft")
		state.Apply(Event{Kind: DoctorOpened, Text: "Diagnostics\nactual warning"})
		if !strings.Contains(state.View("fixture", false).Content, "actual warning") {
			t.Fatal("Doctor is not visible")
		}
		actions := state.HandleKey(Key{Name: key}, time.Now())
		if len(actions) != 0 || state.Doctor != nil || state.Editor.Text() != "retained typed draft" || len(state.Entries) != 1 || state.Entries[0].Text != "Claude Code diagnostics dismissed" {
			t.Fatal("Doctor dismiss changed draft or submitted ASK", key, actions, state.Doctor, state.Entries)
		}
	}
	state := NewState(nil, 40, 8)
	state.Apply(Event{Kind: DoctorOpened, Text: strings.Repeat("line\n", 40) + "FINAL_DIAGNOSTIC"})
	state.HandleKey(Key{Name: "ctrl+end"}, time.Now())
	if !strings.Contains(state.View("fixture", false).Content, "FINAL_DIAGNOSTIC") {
		t.Fatal("long Doctor hides final diagnosis")
	}
	state.HandleKey(Key{Name: "ctrl+home"}, time.Now())
	if strings.Contains(state.View("fixture", false).Content, "FINAL_DIAGNOSTIC") {
		t.Fatal("Doctor did not scroll to first diagnosis")
	}
	accept := "confirm:yes"
	state.SetKeybindings([]Keybinding{{Context: "Confirmation", Chord: ParseChord("ctrl+k d"), Action: &accept}})
	state.HandleKey(Key{Name: "enter"}, time.Now())
	if state.Doctor == nil {
		t.Fatal("unbound Enter overrides Source customization")
	}
	state.HandleKey(Key{Name: "ctrl+k"}, time.Now())
	state.HandleKey(Key{Name: "d"}, time.Now())
	if state.Doctor != nil {
		t.Fatal("custom confirmation chord cannot close Doctor")
	}
}
