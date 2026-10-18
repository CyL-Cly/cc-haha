package tui

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Delivered mail belongs to the conversation. The independent spinner preview
// preference controls running-task previews, not these retained messages.
func terminalTeammateMessageLines(row protocol.JSONObject, width int, theme string, transcript bool, origins ...TeammateMessageOrigin) []string {
	origin := TeammateMessageOrigin("")
	if len(origins) > 0 {
		origin = origins[0]
	}
	if rows, handled := terminalTeammateNotificationLines(row, width, theme, origin); handled {
		return rows
	}
	if origin == TeammateMessageAttachment {
		if content, ok := terminalTeammateAttachmentSummary(row.String("text")); ok {
			row = row.Clone()
			value, _ := json.Marshal(content)
			row.Set("text", value)
		}
	}
	return terminalTeammatePlainMessageLines(row, width, theme, transcript)
}

func terminalTeammatePlainMessageLines(row protocol.JSONObject, width int, theme string, transcript bool) []string {
	color := row.String("color")
	role := color + "_FOR_SUBAGENTS_ONLY"
	if color == "" {
		role = "cyan_FOR_SUBAGENTS_ONLY"
	}
	style := TerminalThemeRoleEscape(theme, role, false)
	if style == "" && color != "" {
		style = terminalThemeColorEscape("ansi:"+color, false)
	}
	sender := style + displayText("@"+row.String("from")+"❯") + "\x1b[0m"
	parts := []string{sender}
	if summary := row.String("summary"); summary != "" {
		parts = append(parts, displayText(" "+summary))
	}
	// TeammateMessageContent uses two sibling Text nodes in a row. Their flex
	// bases shrink independently; joining their strings changes narrow layouts.
	rows := append([]string{""}, terminalTeammateHeaderLines(parts, width)...)
	if transcript {
		for _, line := range terminalMarkdownWrap(row.String("text"), max(1, width-2)) {
			rows = append(rows, strings.Repeat(" ", min(2, max(0, width)))+line)
		}
	}
	return rows
}
