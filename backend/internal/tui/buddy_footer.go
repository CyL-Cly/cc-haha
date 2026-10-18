package tui

import (
	"time"
)

func (s *State) terminalBuddyFooterVisible() bool {
	if s.buddy == nil || string(s.buddy.value.Get("muted")) == "true" {
		return false
	}
	_, present := s.buddy.value.Object("companion")
	return present
}

func (s *State) terminalBuddyFirstFooter() bool {
	if s.paneTeam || s.RemoteState != nil {
		return false
	}
	for _, property := range s.BackgroundTasks.OwnProperties() {
		value := presentationObject(property.Value)
		if value.String("status") == "running" {
			return false
		}
	}
	return true
}

func (s *State) handleTerminalBuddyFooterKey(key Key, now time.Time) (bool, []action) {
	return s.handleTerminalFooterKey(key, now)
}
