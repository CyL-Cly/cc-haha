package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (s *State) terminalFullscreenHelpView(footer []string) tea.View {
	body := s.transcriptLines()
	if teammate, viewing := s.teammateTranscriptLines(); viewing {
		body = teammate
	}
	available := max(0, s.Height-1)
	end := max(min(len(body)-s.Scroll, len(body)), min(available, len(body)))
	start := max(0, end-available)
	viewport := make([]string, s.Height)
	copy(viewport[min(1, s.Height):], body[start:end])
	copy(viewport[max(0, s.Height-len(footer)):], footer)
	view := tea.NewView(strings.Join(viewport, "\n"))
	view.AltScreen, view.ReportFocus = true, true
	return view
}

func (s *State) retireFullscreenHelpProvisionalInput(event Event) {
	if !s.fullscreen || event.TurnID == "" || event.TurnID != s.ActiveTurn || s.LocalPanel == nil || !s.LocalPanel.props.HelpDialog || s.LocalPanel.props.Kind != "help" {
		return
	}
	retained := make([]Entry, 0, len(s.Entries))
	for _, entry := range s.Entries {
		if entry.TurnID == event.TurnID && entry.Kind == "user" {
			if command := ParseSlashCommand(entry.Text); command != nil && command.CommandName == "help" {
				continue
			}
		}
		retained = append(retained, entry)
	}
	s.Entries = retained
}
