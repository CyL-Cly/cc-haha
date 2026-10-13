package tui

import (
	"testing"
	"time"
)

func TestIDESelectionPreferenceDialogsCancelAndOnboardingYieldToInputPermissions(t *testing.T) {
	state := NewState(nil, 100, 30)
	now := time.Now()
	props := IDEPicker{ID: "physical-owner-choice", Title: "Select IDE", Current: "None", AskAutoConnect: true, Options: []IDEOption{{Value: "123", Label: "Private IDE"}, {Value: "None", Label: "None"}}}
	state.OpenIDEPicker(props)
	state.HandleKey(Key{Name: "up"}, now)
	if state.IDEPicker.selected != 0 {
		t.Fatal("current IDE selection wasn't restored")
	}
	if actions := state.HandleKey(Key{Name: "enter"}, now); len(actions) != 0 || state.IDEPicker.phase != "auto" {
		t.Fatal("choice bypassed original autoConnect dialog", actions)
	}
	if actions := state.HandleKey(Key{Name: "esc"}, now); len(actions) != 0 || state.IDEPicker == nil {
		t.Fatal("original repeated-unset-preference behavior changed")
	}
	state.HandleKey(Key{Name: "down"}, now)
	actions := state.HandleKey(Key{Name: "enter"}, now)
	if len(actions) != 1 || actions[0].idePickerResult.AutoConnect == nil || *actions[0].idePickerResult.AutoConnect || actions[0].idePickerResult.Value != "123" {
		t.Fatal("No preference must still select the IDE", actions)
	}
	props.AskAutoConnect = false
	props.AskDisableAutoConnect = true
	state.OpenIDEPicker(props)
	state.HandleKey(Key{Name: "enter"}, now)
	actions = state.HandleKey(Key{Name: "esc"}, now)
	if len(actions) != 1 || actions[0].idePickerResult.Value != "None" || actions[0].idePickerResult.DisableAutoConnect {
		t.Fatal("cancelled disable preference must still disconnect", actions)
	}
	state.OpenIDEPicker(IDEPicker{ID: "native-onboarding", Mode: "onboarding", Title: "Welcome"})
	state.pluginHintInputChanged(now)
	state.Editor.Set("typing")
	state.pluginHintInputChanged(now)
	if state.ideOnboardingVisible() || len(state.ideOnboardingTick()) != 0 {
		t.Fatal("onboarding interrupted actual prompt typing")
	}
	state.pluginHintTick(now.Add(2 * time.Second))
	state.Permissions = []PermissionRequest{{ID: "actual-permission", ToolName: "Write"}}
	if state.ideOnboardingVisible() || len(state.ideOnboardingTick()) != 0 {
		t.Fatal("onboarding took priority over original permission queue")
	}
	state.Permissions = nil
	actions = state.ideOnboardingTick()
	if len(actions) != 1 || !actions[0].idePickerResult.Presented || len(state.ideOnboardingTick()) != 0 {
		t.Fatal("rendered onboarding must publish exactly one actual mark-shown callback", actions)
	}
	actions = state.HandleKey(Key{Name: "enter"}, now.Add(2*time.Second))
	if len(actions) != 1 || state.IDEOnboarding != nil {
		t.Fatal("onboarding did not dismiss", actions)
	}
}
