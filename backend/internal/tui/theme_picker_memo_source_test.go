package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestThemePickerOriginalReasonAndSyntaxThemeMemoSurviveEnvironmentRerenders(t *testing.T) {
	disabledEnv := "0"
	cases := []actualThemeCase{
		{Name: "allowed-then-env-disabled", Theme: "dark", Width: 80, User: map[string]any{}, Steps: []map[string]any{{"action": "env", "value": "0"}, {"action": "focus", "theme": "light"}, {"action": "toggle"}, {"action": "toggle"}}},
		{Name: "disabled-then-env-unset", Theme: "dark", SyntaxEnv: &disabledEnv, Width: 80, User: map[string]any{}, Steps: []map[string]any{{"action": "env", "value": nil}, {"action": "focus", "theme": "light"}, {"action": "toggle"}}},
	}
	rows := actualThemeFixture(t, cases)
	for index, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			state := NewState(nil, 80, 32)
			props := LocalPanel{ID: "same-picker", Kind: "theme", ThemePicker: true, SelectedRow: "dark"}
			for _, option := range TerminalThemeOptions(false) {
				props.Rows = append(props.Rows, LocalPanelRow{ID: option.ID, Label: option.Label})
			}
			mountReason, currentReason, env := "", "", "undefined"
			if item.SyntaxEnv != nil {
				mountReason, currentReason, env = "env", "env", *item.SyntaxEnv
			}
			disabled := false
			publish := func() {
				props.Presentation, _ = json.Marshal(map[string]any{"colorModuleUnavailableReason": mountReason, "currentColorModuleUnavailableReason": currentReason, "syntaxEnvironment": env, "syntaxHighlightingDisabled": disabled})
				state.openLocalPanel(&props)
			}
			publish()
			_ = state.LocalPanel.terminalThemePickerLines(80, 32)
			var observations []protocol.JSONObject
			if err := json.Unmarshal(rows[index].Get("observations"), &observations); err != nil {
				t.Fatal(err)
			}
			for stepIndex, step := range item.Steps {
				switch step["action"] {
				case "env":
					if step["value"] == nil {
						currentReason, env = "", "undefined"
					} else {
						currentReason, env = "env", "0"
					}
					publish()
				case "focus":
					state.HandleKey(Key{Name: "down"}, time.Now())
				case "toggle":
					actions := state.HandleKey(Key{Name: "ctrl+t"}, time.Now())
					if mountReason == "env" {
						if len(actions) != 0 {
							t.Fatal("cached env gate changed", actions)
						}
					} else {
						if len(actions) != 1 || actions[0].panel.Action != "theme-toggle-syntax" {
							t.Fatal("actual default syntax binding absent", actions)
						}
						disabled = !disabled
						publish()
					}
				}
				view := strings.Join(state.LocalPanel.terminalThemePickerLines(80, 32), "\n")
				if label := observations[stepIndex].String("label"); !strings.Contains(view, label) {
					t.Fatalf("Source memo label differs afterstep%d expected%q got%s", stepIndex, label, view)
				}
			}
		})
	}
}

func TestThemePickerCustomBindingCaptionAndPendingChordUseActualConsumer(t *testing.T) {
	state := NewState(nil, 80, 32)
	state.SetKeybindings(append(DefaultKeybindings("macos", true, KeybindingFeatures{}), binding("ThemePicker", "ctrl+k t", "theme:toggleSyntaxHighlighting")))
	props := LocalPanel{ID: "custom", Kind: "theme", ThemePicker: true, SelectedRow: "dark"}
	for _, option := range TerminalThemeOptions(false) {
		props.Rows = append(props.Rows, LocalPanelRow{ID: option.ID, Label: option.Label})
	}
	state.openLocalPanel(&props)
	if !strings.Contains(state.View("", false).Content, "ctrl+k t to disable") {
		t.Fatal("actual custom shortcut caption missing")
	}
	if actions := state.HandleKey(Key{Name: "ctrl+k"}, time.Now()); len(actions) != 0 {
		t.Fatal(actions)
	}
	if actions := state.HandleKey(Key{Name: "t", Text: "t"}, time.Now()); len(actions) != 1 || actions[0].panel.Action != "theme-toggle-syntax" {
		t.Fatal("actual pending chord did not toggle", actions)
	}
}
