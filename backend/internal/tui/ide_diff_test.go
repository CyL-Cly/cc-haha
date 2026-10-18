package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIDEDiffPermissionUpdateUsesOnlyExistingRequestAndPreservesSelection(t *testing.T) {
	s := NewState(nil, 100, 30)
	s.Editor.Set("perform actual edit")
	input := key(s, "enter")[0].input
	request := PermissionRequest{ID: "actual-diff-1", ToolName: "Edit", Input: json.RawMessage(`{"file_path":"diff.txt","old_string":"old","new_string":"new"}`), Choices: []PermissionChoice{{Key: "y", Label: "Allow once", Decision: PermissionDecision{Allowed: true}}, {Key: "n", Label: "Deny", Decision: PermissionDecision{Allowed: false}}}}
	s.Apply(Event{TurnID: input.TurnID, Kind: PermissionAsked, Permission: &request})
	s.PermissionSelected = 1
	mounted := request
	mounted.IDEName = "Actual Diff IDE"
	s.Apply(Event{Kind: PermissionUpdated, Permission: &mounted})
	if len(s.Permissions) != 1 || s.Permissions[0].IDEName != mounted.IDEName || s.PermissionSelected != 1 || !strings.Contains(s.View("", false).Content, "Opened changes in Actual Diff IDE") {
		t.Fatal("mounted IDE update lost current request/choice", s.Permissions, s.PermissionSelected)
	}
	unknown := mounted
	unknown.ID = "retired-other-request"
	s.Apply(Event{Kind: PermissionUpdated, Permission: &unknown})
	if len(s.Permissions) != 1 || s.Permissions[0].ID != request.ID {
		t.Fatal("late update opened a new permission", s.Permissions)
	}
	mounted.IDEName = ""
	s.Apply(Event{Kind: PermissionUpdated, Permission: &mounted})
	if s.PermissionSelected != 1 || s.Permissions[0].IDEName != "" || strings.Contains(s.View("", false).Content, "Opened changes in") {
		t.Fatal("terminal fallback lost selected denial", s.Permissions, s.PermissionSelected)
	}
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].kind != actionPermission || actions[0].permission != request.ID || actions[0].decision.Allowed {
		t.Fatal("fallback did not use original actual permission choice", actions)
	}
	s.Apply(Event{Kind: PermissionClosed, Permission: &request})
	mounted.IDEName = "Actual Diff IDE"
	s.Apply(Event{Kind: PermissionUpdated, Permission: &mounted})
	if len(s.Permissions) != 0 {
		t.Fatal("retired mount reopened after actual response", s.Permissions)
	}
}
