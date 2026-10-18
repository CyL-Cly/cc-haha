package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPermissionDebugActualConfirmationConsumerUnmountsChoicesAndRetiresID(t *testing.T) {
	for _, tool := range []string{"Bash", "PowerShell"} {
		t.Run(tool, func(t *testing.T) {
			s := NewState(nil, 100, 35)
			s.Editor.Set("actual pending command")
			input := key(s, "enter")[0].input
			request := PermissionRequest{ID: "debug-request", ToolName: tool, Debug: &PermissionDebugData{Result: json.RawMessage(`{"behavior":"ask","message":"Actual permission","decisionReason":{"type":"other","reason":"Cached actual policy"}}`), Context: json.RawMessage(`{}`)}, Choices: []PermissionChoice{{Key: "y", Label: "Allow once", Decision: PermissionDecision{Allowed: true}}, {Key: "n", Label: "Deny", Decision: PermissionDecision{Allowed: false}}, {Key: "e", Label: "Edit rule", Editable: true}}}
			s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: &request})
			s.PermissionSelected = 1
			if got := key(s, "ctrl+d"); len(got) != 0 || !s.PermissionDebugVisible(request) {
				t.Fatal("actual unconditional Confirmation toggle missing", got)
			}
			view := s.View("", false).Content
			if !strings.Contains(view, "Cached actual policy") || strings.Contains(view, "Allow once") || strings.Contains(view, "to show debug info") {
				t.Fatal("Select was not unmounted", view)
			}
			for _, name := range []string{"enter", "y", "n", "esc", "up", "down", "tab", "e"} {
				if got := key(s, name); len(got) != 0 || len(s.Permissions) != 1 || s.PermissionSelected != 1 || s.permissionEditor != nil {
					t.Fatal("debug key consumed approval/edit", name, got, s.Permissions)
				}
			}
			updated := request
			updated.Description = "same-ID runtime update"
			s.Apply(Event{Kind: PermissionUpdated, Permission: &updated})
			if !s.PermissionDebugVisible(updated) {
				t.Fatal("same-ID update lost view")
			}
			key(s, "ctrl+d")
			if s.PermissionDebugVisible(request) || !strings.Contains(s.View("", false).Content, "Allow once") {
				t.Fatal("toggle did not remount Select")
			}
			key(s, "ctrl+d")
			s.Apply(Event{Kind: PermissionClosed, Permission: &request})
			s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: &request})
			if s.PermissionDebugVisible(request) {
				t.Fatal("retired ID reopened debug")
			}
			key(s, "ctrl+d")
			if got := key(s, "ctrl+c"); len(got) != 1 || got[0].kind != actionCancel || len(s.Permissions) != 0 {
				t.Fatal("global cancel was inhibited", got)
			}
		})
	}
	s := NewState(nil, 100, 35)
	request := PermissionRequest{ID: "not-shell", ToolName: "Write", Debug: &PermissionDebugData{DebugHint: true}, Choices: []PermissionChoice{{Key: "y", Label: "Allow once", Decision: PermissionDecision{Allowed: true}}}}
	s.Permissions = []PermissionRequest{request}
	if s.TogglePermissionDebug() || s.PermissionDebugVisible(request) {
		t.Fatal("non-shell entered debug")
	}
	if got := key(s, "ctrl+d"); len(got) != 0 {
		t.Fatal("unregistered non-shell debug action escaped to model/action host", got)
	}
}
