package tui

import "context"

type TerminalPreferences struct {
	UpdateChannel                                                string
	EditorMode                                                   string
	Theme                                                        string
	SpinnerTip                                                   string
	ShowTips                                                     bool
	PRStatusFooterEnabled                                        bool
	Verbose, ShowTurnDuration, ReducedMotion, ProgressBarEnabled bool
}
type TerminalPreferenceSource interface {
	TerminalPreferences(context.Context) (TerminalPreferences, error)
}

func (s *State) SetTerminalPreferences(preferences TerminalPreferences) {
	s.Preferences = preferences
}
func (s *State) themeAccent() string {
	theme := s.previewTheme()
	if theme == "auto" {
		if s.LightBackground {
			theme = "light"
		} else {
			theme = "dark"
		}
	}
	return TerminalThemeRoleEscape(theme, "claude", false)
}

func (s *State) promptAccent() string {
	switch s.AgentColor {
	case "blue":
		return "\x1b[34m"
	case "red":
		return "\x1b[31m"
	case "yellow":
		return "\x1b[33m"
	case "purple":
		return "\x1b[35m"
	case "green":
		return "\x1b[32m"
	case "orange":
		return "\x1b[38;5;208m"
	case "pink":
		return "\x1b[38;5;205m"
	case "cyan":
		return "\x1b[36m"
	}
	return s.themeAccent()
}
