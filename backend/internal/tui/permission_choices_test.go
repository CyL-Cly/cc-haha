package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPowerShellPermissionPrefixEditorRetainsDraftAndApprovesOnlyExplicitEnter(t *testing.T) {
	s := NewState(nil, 100, 24)
	s.Editor.Set("preserved user draft")
	request := PermissionRequest{ID: "owned-prefix", ToolName: "PowerShell", Choices: []PermissionChoice{{Key: "y", Label: "Yes", Decision: PermissionDecision{Allowed: true}}, {Label: "Yes, and don’t ask again for", Editable: true, InitialValue: "Remove-Item:*", Decision: PermissionDecision{Allowed: true}}, {Key: "n", Label: "No", Decision: PermissionDecision{Allowed: false}}}}
	s.Apply(Event{Kind: PermissionAsked, Permission: &request})
	if len(key(s, "down")) != 0 {
		t.Fatal("selection approved")
	}
	if len(key(s, "ctrl+u")) != 0 {
		t.Fatal("editing approved")
	}
	for _, name := range []string{"n", "j", "k", "y"} {
		if len(key(s, name)) != 0 {
			t.Fatal("typed prefix invoked approval shortcut", name)
		}
	}
	s.HandleKey(Key{Name: "text", Text: " 中文😀:*"}, time.Now())
	if !strings.Contains(s.View("", false).Content, "njky 中文😀:*") {
		t.Fatal("actual edited Unicode prefix not rendered", s.View("", false).Content)
	}
	if len(key(s, "up")) != 0 || len(key(s, "down")) != 0 {
		t.Fatal("navigation approved")
	}
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].permission != request.ID || !actions[0].decision.Allowed || actions[0].decision.EditedPrefix == nil || *actions[0].decision.EditedPrefix != "njky 中文😀:*" {
		t.Fatal("actual user edit lost", actions)
	}
	if s.Editor.Text() != "preserved user draft" || len(s.Permissions) != 0 {
		t.Fatal("permission edit changed composer or left pending state")
	}
}
func TestPowerShellPrefixEmptyDenyAndStalePendingLifetimes(t *testing.T) {
	for _, submit := range []string{"enter", "esc"} {
		s := NewState(nil, 80, 24)
		request := PermissionRequest{ID: "prefix", Choices: []PermissionChoice{{Editable: true, InitialValue: "literal", Decision: PermissionDecision{Allowed: true}}}}
		s.Apply(Event{Kind: PermissionAsked, Permission: &request})
		key(s, "ctrl+u")
		actions := key(s, submit)
		if len(actions) != 1 {
			t.Fatal(actions)
		}
		if submit == "enter" {
			if !actions[0].decision.Allowed || actions[0].decision.EditedPrefix == nil || *actions[0].decision.EditedPrefix != "" {
				t.Fatal("empty prefix is explicit allow once", actions)
			}
		} else if actions[0].decision.Allowed || actions[0].decision.EditedPrefix != nil {
			t.Fatal("denial persisted an edit", actions)
		}
	}
	s := NewState(nil, 80, 24)
	request := PermissionRequest{ID: "old", Choices: []PermissionChoice{{Editable: true, InitialValue: "original", Decision: PermissionDecision{Allowed: true}}}}
	s.Apply(Event{Kind: PermissionAsked, Permission: &request})
	key(s, "ctrl+u")
	s.Apply(Event{Kind: PermissionClosed, Permission: &request})
	request.ID = "new"
	request.Choices[0].InitialValue = "new prefix"
	s.Apply(Event{Kind: PermissionAsked, Permission: &request})
	actions := key(s, "enter")
	if len(actions) != 1 || actions[0].decision.EditedPrefix == nil || *actions[0].decision.EditedPrefix != "new prefix" {
		t.Fatal("retired permission editor leaked", actions)
	}
}

func TestPowerShellPrefixEditorCursorTracksGraphemesAndWrappedChoice(t *testing.T) {
	s := NewState(nil, 24, 24)
	request := PermissionRequest{ID: "cursor", ToolName: "PowerShell", Choices: []PermissionChoice{{Label: "Remember", Editable: true, InitialValue: "Get-Content 中文😀:*", Decision: PermissionDecision{Allowed: true}}}}
	s.Apply(Event{Kind: PermissionAsked, Permission: &request})
	key(s, "end")
	end := s.View("", false).Cursor
	if end == nil {
		t.Fatal("editable rule has no terminal cursor")
	}
	key(s, "left")
	key(s, "left")
	key(s, "left") // The entire emoji is one editable grapheme and two display cells.
	beforeEmoji := s.View("", false).Cursor
	if beforeEmoji == nil || beforeEmoji.Y != end.Y || beforeEmoji.X != end.X-4 {
		t.Fatalf("visible cursor split the emoji or lost wrapping: end=%+v beforeEmoji=%+v", end, beforeEmoji)
	}
	key(s, "ctrl+a")
	start := s.View("", false).Cursor
	if start == nil || start.X != 12 || start.Y != end.Y-1 {
		t.Fatalf("wrapped edit cursor no longer points after the choice label: %+v end=%+v", start, end)
	}
	key(s, "esc")
	if len(s.Permissions) != 0 || s.permissionEditor != nil {
		t.Fatal("closed approval retained its editor")
	}
}
