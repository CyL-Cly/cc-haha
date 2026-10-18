package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (s *State) terminalHelpTranscriptView(fullscreen bool) tea.View {
	toggle, ok := BindingDisplay("app:toggleTranscript", "Global", s.bindings)
	if !ok {
		toggle = "ctrl+o"
	}
	label := "Showing detailed transcript · " + toggle + " to toggle"
	if fullscreen {
		label += " · ↑↓ scroll · home/end top/bottom"
	} else {
		shortcut, ok := BindingDisplay("transcript:toggleShowAll", "Transcript", s.bindings)
		if !ok {
			shortcut = "ctrl+e"
		}
		verb := "show all"
		if s.transcriptShowAll {
			verb = "collapse"
		}
		label += " · " + shortcut + " to " + verb
	}
	inactive := TerminalThemeRoleEscape(s.previewTheme(), "inactive", false)
	footer := []string{"", "\x1b[2m" + strings.Repeat("─", s.Width) + "\x1b[22m"}
	for _, line := range exportWrapWords(label, max(1, s.Width-2)) {
		footer = append(footer, "  "+inactive+line+"\x1b[39m")
	}
	body := s.transcriptLines()
	body = append(body, s.LocalPanel.terminalHelpLines(s.Width, s.Height, s.previewTheme(), s.bindings)...)
	if fullscreen {
		body = append([]string{""}, body...)
	}
	available := max(0, s.Height-len(footer))
	end := max(min(len(body)-s.Scroll, len(body)), min(available, len(body)))
	start := max(0, end-available)
	lines := append([]string(nil), body[start:end]...)
	if fullscreen && len(lines) < s.Height-len(footer) {
		lines = append(lines, make([]string, s.Height-len(footer)-len(lines))...)
	}
	lines = append(lines, footer...)
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen, view.ReportFocus = fullscreen, true
	return view
}
