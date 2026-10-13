package tui

import (
	"time"
	"unicode/utf8"
)

type permissionChoiceEditor struct {
	id     string
	index  int
	editor Editor
}

func (s *State) permissionEditableSelected() bool {
	return len(s.Permissions) > 0 && s.PermissionSelected < len(s.Permissions[0].Choices) && s.Permissions[0].Choices[s.PermissionSelected].Editable
}
func (s *State) selectedPermissionEditor() *permissionChoiceEditor {
	request := s.Permissions[0]
	index := s.PermissionSelected
	if s.permissionEditor == nil || s.permissionEditor.id != request.ID || s.permissionEditor.index != index {
		editor := &permissionChoiceEditor{id: request.ID, index: index}
		editor.editor.Set(request.Choices[index].InitialValue)
		s.permissionEditor = editor
	}
	return s.permissionEditor
}
func (s *State) startPermissionChoiceEditor(request PermissionRequest, index int) []action {
	s.PermissionSelected = index
	_ = s.selectedPermissionEditor()
	return nil
}
func (s *State) handlePermissionChoiceEditor(key Key) (bool, []action) {
	if !s.permissionEditableSelected() {
		return false, nil
	}
	return true, s.handlePermissionChoiceEditorKey(key, time.Time{})
}
func (s *State) handlePermissionChoiceEditorKey(key Key, now time.Time) []action {
	request := s.Permissions[0]
	editor := s.selectedPermissionEditor()
	switch key.Name {
	case "up", "down", "tab", "shift+tab":
		delta := 1
		if key.Name == "up" || key.Name == "shift+tab" {
			delta = -1
		}
		s.PermissionSelected = (s.PermissionSelected + delta + len(request.Choices)) % len(request.Choices)
		return nil
	case "enter":
		decision := request.Choices[editor.index].Decision
		prefix := editor.editor.Text()
		decision.EditedPrefix = &prefix
		s.Permissions = s.Permissions[1:]
		s.PermissionSelected = 0
		s.permissionEditor = nil
		return []action{{kind: actionPermission, permission: request.ID, decision: decision}}
	case "esc":
		s.Permissions = s.Permissions[1:]
		s.PermissionSelected = 0
		s.permissionEditor = nil
		return []action{{kind: actionPermission, permission: request.ID, decision: PermissionDecision{Allowed: false}}}
	case "ctrl+c":
		return s.cancel()
	case "left", "ctrl+b":
		editor.editor.Left()
	case "right", "ctrl+f":
		editor.editor.Right()
	case "ctrl+left", "alt+left", "alt+b":
		editor.editor.WordLeft()
	case "ctrl+right", "alt+right", "alt+f":
		editor.editor.WordRight()
	case "home", "ctrl+a":
		editor.editor.Home()
	case "end", "ctrl+e":
		editor.editor.End()
	case "backspace", "ctrl+h":
		editor.editor.Backspace()
	case "delete", "ctrl+d":
		editor.editor.Delete()
	case "ctrl+u":
		editor.editor.KillToStart()
	case "ctrl+k":
		editor.editor.KillToEnd()
	case "ctrl+w", "alt+backspace":
		editor.editor.KillWordBefore()
	case "alt+d":
		editor.editor.KillWordAfter()
	case "ctrl+y":
		editor.editor.Yank()
	case "alt+y":
		editor.editor.YankPop()
	case "ctrl+_", "ctrl+z":
		editor.editor.Undo()
	case "paste":
		editor.editor.Paste(key.Text)
	default:
		if key.Text != "" {
			editor.editor.Insert(key.Text)
		} else if utf8.RuneCountInString(key.Name) == 1 {
			editor.editor.Insert(key.Name)
		}
	}
	return nil
}
func (s *State) permissionChoiceValue(request PermissionRequest, index int) string {
	if s.permissionEditor != nil && s.permissionEditor.id == request.ID && s.permissionEditor.index == index {
		return s.permissionEditor.editor.Text()
	}
	return request.Choices[index].InitialValue
}
