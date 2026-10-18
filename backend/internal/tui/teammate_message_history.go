package tui

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

// The stored user record remains the original XML. Rendering samples the same
// teams gate as the mounted native task source, so a later snapshot does not
// require rewriting or reloading historical messages.
func (s *State) terminalTeammateHistoricalUserLines(text string, index int) ([]string, bool) {
	if !strings.Contains(text, "<teammate-message") {
		return nil, false
	}
	// The original UserMessage caches its UserTextMessage element. An env-only
	// update keeps that element; actual transcript/verbose props resample its
	// teams gate. Width and theme still reflow/repaint the retained subtree.
	cache := s.Entries[index].teammateUser
	if cache == nil || cache.text != text || cache.transcript != s.Transcript || cache.verbose != s.Preferences.Verbose {
		cache = &terminalTeammateUserProjection{text: text, transcript: s.Transcript, verbose: s.Preferences.Verbose, enabled: s.taskTeamsEnabled}
		s.Entries[index].teammateUser = cache
	}
	if !cache.enabled {
		return terminalTeammateRawUserMessageLines(text, s.Width, s.previewTheme()), true
	}
	rows := []string{}
	for _, parsed := range teamspawn.ParseInteractiveTeammateXML(text) {
		message := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: parsed.Get("teammateId")}, protocol.JSONField{Name: "text", Value: parsed.Get("content")})
		for _, key := range []string{"color", "summary"} {
			if value := parsed.Get(key); value != nil {
				message.Set(key, value)
			}
		}
		if teammateVisible(message) {
			rows = append(rows, terminalTeammateMessageLines(message, s.Width, s.previewTheme(), s.Transcript)...)
		}
	}
	return rows, true
}

// Ephemeral mounted UserTextMessage state; never serialized with an Entry.
type terminalTeammateUserProjection struct {
	text                         string
	transcript, verbose, enabled bool
}

func terminalTeammateRawUserMessageLines(text string, width int, theme string) []string {
	background := "rgb(55,55,55)"
	switch theme {
	case "light":
		background = "rgb(240,240,240)"
	case "light-daltonized":
		background = "rgb(220,220,220)"
	case "dark-ansi":
		background = "ansi:blackBright"
	case "light-ansi":
		background = "ansi:white"
	}
	bg := strings.TrimSuffix(strings.TrimPrefix(terminalThemeColorEscape(background, true), "\x1b["), "m")
	foreground := func(role string) string {
		return strings.TrimSuffix(strings.TrimPrefix(TerminalThemeRoleEscape(theme, role, false), "\x1b["), "m")
	}
	rows := exportWrapWords("❯ "+displayText(text), max(1, width-1))
	for index, row := range rows {
		painted := strings.Builder{}
		position := 0
		for _, cell := range terminalStatusLineParse(row, false, foreground("text")) {
			cell.style.bg = bg
			if index == 0 && position < 2 {
				cell.style.fg = foreground("subtle")
			}
			painted.WriteString("\x1b[0;" + cell.style.sgr() + "m" + cell.text + "\x1b[0m")
			position += exportWidth(cell.text)
		}
		if remaining := width - position; remaining > 0 {
			painted.WriteString("\x1b[0;39;" + bg + "m" + strings.Repeat(" ", remaining) + "\x1b[0m")
		}
		rows[index] = painted.String()
	}
	return rows
}
