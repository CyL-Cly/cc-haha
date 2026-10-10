package tui

import "testing"

func TestIdlePermissionEventsAreKeyedByPendingIDAndDoNotAdmitStaleModelEvents(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Apply(Event{Kind: PermissionAsked, Permission: &PermissionRequest{ID: "true-pending", ToolName: "Write"}})
	if len(s.Permissions) != 1 {
		t.Fatal("genuine idle pending permission lost")
	}
	s.Apply(Event{TurnID: "past-human", Kind: PermissionAsked, Permission: &PermissionRequest{ID: "true-pending", ToolName: "Write"}})
	if len(s.Permissions) != 1 {
		t.Fatal("same pending request duplicated")
	}
	s.Apply(Event{TurnID: "past-human", Kind: TextDelta, Text: "stale model"})
	if len(s.Entries) != 0 {
		t.Fatal("permission independence admitted stale model event")
	}
	s.Apply(Event{Kind: PermissionClosed, Permission: &PermissionRequest{ID: "true-pending"}})
	if len(s.Permissions) != 0 {
		t.Fatal("idle pending close lost")
	}
}
