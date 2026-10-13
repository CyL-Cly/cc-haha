package tui

import (
	"encoding/json"
	"testing"
)

func TestAutoConsentTerminalAdvanceAndAcceptanceUseOriginalHostChoices(t *testing.T) {
	request := &PermissionRequest{ID: "owned-consent", ToolName: "AutoModeConsent", Title: "Enable auto mode?", Choices: []PermissionChoice{{Key: "1", Label: "Yes, and make it my default mode", Decision: PermissionDecision{Allowed: true, Rule: json.RawMessage(`"accept-default"`)}}, {Key: "2", Label: "Yes, enable auto mode", Decision: PermissionDecision{Allowed: true}}, {Key: "3", Label: "No, go back", Decision: PermissionDecision{Allowed: false}}}}
	state := NewState(nil, 80, 24)
	state.applyInitialAutoConsent(request)
	actions := key(state, "shift+tab")
	if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != "chat:cycleMode" || len(state.Permissions) != 0 {
		t.Fatal("Shift+Tab must advance without re-entry", actions, state.Permissions)
	}
	state.applyInitialAutoConsent(request)
	actions = key(state, "enter")
	if len(actions) != 1 || actions[0].kind != actionPermission || actions[0].permission != request.ID || string(actions[0].decision.Rule) != `"accept-default"` {
		t.Fatal("default acceptance lost original settings action", actions)
	}
	state.applyInitialAutoConsent(request)
	actions = key(state, "esc")
	if len(actions) != 1 || actions[0].decision.Allowed {
		t.Fatal("decline granted consent", actions)
	}
}
