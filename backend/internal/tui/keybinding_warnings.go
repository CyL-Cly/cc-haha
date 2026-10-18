package tui

import "time"

type KeybindingWarningSource interface{ KeybindingWarningsSummary() string }

func (s *State) SetKeybindingWarning(text string, now time.Time) {
	s.keybindingWarning = text
	s.keybindingWarningUntil = now.Add(60 * time.Second)
}
