package tui

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrStartupConsentDeclined = errors.New("Auto mode startup consent declined")

type TerminalAutoConsentSource interface {
	BindTerminalAutoConsent(context.Context, Emit) (*PermissionRequest, func(), error)
}

func (s *State) handleAutoConsentKey(key Key) (bool, []action) {
	if len(s.Permissions) == 0 || s.Permissions[0].ToolName != "AutoModeConsent" {
		return false, nil
	}
	var payload struct{ Startup bool }
	_ = json.Unmarshal(s.Permissions[0].Payload, &payload)
	if payload.Startup {
		return false, nil
	}
	result := ResolveChord(keyStroke(key), []string{"Global", "Chat"}, s.bindings, nil)
	if result.Type == "match" && result.Action == "chat:cycleMode" {
		request := s.Permissions[0]
		s.Permissions = s.Permissions[1:]
		s.PermissionSelected = 0
		return true, []action{{kind: actionInvoke, name: "chat:cycleMode", permission: request.ID}}
	}
	return false, nil
}

func (s *State) applyInitialAutoConsent(request *PermissionRequest) {
	if request != nil {
		s.Apply(Event{Kind: PermissionAsked, Permission: request})
	}
}
