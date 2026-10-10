package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestModelPickerOriginalSelectNavigationEffortNumericAndDraftIsolation(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("draft 中文👋")
	options := []ModelOption{{Label: "Default", SupportsEffort: true, DefaultEffort: "medium"}}
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("model-%d", i)
		options = append(options, ModelOption{Value: &name, Label: name, SupportsEffort: i != 12, SupportsMax: i == 11, DefaultEffort: "high"})
	}
	initial := "model-11"
	s.OpenModelPicker(ModelPicker{ID: "native-menu", Initial: &initial, Options: options, Standalone: true})
	if s.ModelPicker.focused != 11 || s.ModelPicker.from != 2 || s.ModelPicker.to != 12 || *s.ModelPicker.effort != "high" {
		t.Fatal("Source initial viewport/default", s.ModelPicker)
	}
	options[11].Label = "mutated"
	initial = "mutated"
	if s.ModelPicker.props.Options[11].Label != "model-11" || *s.ModelPicker.props.Initial != "model-11" {
		t.Fatal("host alias mutated terminal state")
	}
	now := time.Unix(100, 0)
	s.HandleKey(Key{Name: "right"}, now)
	if *s.ModelPicker.effort != "max" || !s.ModelPicker.toggled {
		t.Fatal("Source max effort cycle", s.ModelPicker)
	}
	s.HandleKey(Key{Name: "down"}, now)
	if s.ModelPicker.focused != 12 || *s.ModelPicker.effort != "max" {
		t.Fatal("toggled effort was reset by focus", s.ModelPicker)
	}
	if !strings.Contains(s.View("", false).Content, "Effort not supported for model-12") {
		t.Fatal("unsupported focused model display")
	}
	s.HandleKey(Key{Name: "down"}, now)
	if s.ModelPicker.focused != 0 || s.ModelPicker.from != 0 || s.ModelPicker.to != 10 {
		t.Fatal("Source next wrapping viewport", s.ModelPicker)
	}
	s.HandleKey(Key{Name: "pagedown"}, now)
	if s.ModelPicker.focused != 10 || s.ModelPicker.from != 1 || s.ModelPicker.to != 11 {
		t.Fatal("Source next page", s.ModelPicker)
	}
	s.HandleKey(Key{Name: "pageup"}, now)
	if s.ModelPicker.focused != 0 || s.ModelPicker.from != 0 || s.ModelPicker.to != 10 {
		t.Fatal("Source previous page", s.ModelPicker)
	}
	actions := s.HandleKey(Key{Name: "１３", Text: "１３"}, now)
	if len(actions) != 1 || actions[0].kind != actionModelPicker || actions[0].modelPickerResult.Model == nil || *actions[0].modelPickerResult.Model != "model-12" || *actions[0].modelPickerResult.Effort != "max" {
		t.Fatal("Source fullwidth absolute numeric callback preserves previously toggled effort", actions)
	}
	if s.ModelPicker != nil || s.Editor.Text() != "draft 中文👋" || s.Busy() || len(s.history) != 0 {
		t.Fatal("picker became prompt or changed editor state")
	}
}

func TestModelPickerCustomContextNullBindingAndCancelNeverSubmit(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("draft")
	s.SetKeybindings(append(DefaultKeybindings("macos", true, KeybindingFeatures{}), binding("Select", "alt+j", "select:next"), Keybinding{Context: "Select", Chord: ParseChord("down")}, binding("ModelPicker", "alt+h", "modelPicker:decreaseEffort")))
	name := "custom"
	s.OpenModelPicker(ModelPicker{ID: "one", Options: []ModelOption{{Label: "Default", SupportsEffort: true, DefaultEffort: "medium"}, {Value: &name, Label: name, SupportsEffort: true, DefaultEffort: "high"}}})
	now := time.Unix(100, 0)
	s.HandleKey(Key{Name: "down"}, now)
	if s.ModelPicker.focused != 0 {
		t.Fatal("null override was bypassed")
	}
	s.HandleKey(Key{Name: "alt+j"}, now)
	if s.ModelPicker.focused != 1 || *s.ModelPicker.effort != "high" {
		t.Fatal("custom Select context not consumed")
	}
	s.HandleKey(Key{Name: "alt+h"}, now)
	if *s.ModelPicker.effort != "medium" {
		t.Fatal("custom ModelPicker context not consumed")
	}
	s.Paste("ignored")
	a := s.HandleKey(Key{Name: "esc"}, now)
	if len(a) != 1 || a[0].kind != actionModelPicker || !a[0].modelPickerResult.Cancelled || s.Editor.Text() != "draft" {
		t.Fatal("cancel/paste leaked to prompt", a)
	}
}
