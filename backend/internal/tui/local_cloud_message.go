package tui

import (
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalCloudLocalOutputLines(value string, width int, theme string) []string {
	header, rest, hasRest := strings.Cut(value, "\n")
	diamond := "◇"
	if strings.HasPrefix(header, "◆ ") {
		diamond = "◆"
	}
	header = strings.TrimPrefix(header, diamond+" ")
	label, suffix, hasSuffix := strings.Cut(header, " · ")
	if hasSuffix {
		suffix = " · " + suffix
	}
	heading := TerminalThemeRoleEscape(theme, "background", false) + diamond + " \x1b[39m" + "\x1b[1m" + displayText(label) + "\x1b[22m"
	if hasSuffix {
		heading += TerminalThemeRoleEscape(theme, "inactive", false) + displayText(suffix) + "\x1b[39m"
	}
	rows := terminalMarkdownWrap(heading, width)
	if !hasRest {
		return rows
	}
	rest = protocol.TrimJavaScriptString(rest)
	if rest == "" {
		return rows
	}
	basis := 0
	for _, row := range exportWrapWords(rest, max(1, width)) {
		basis = max(basis, exportWidth(row))
	}
	left := 5
	if basis+left > width {
		// Unlike the ordinary output's right Box, this direct Text child paints
		// at the floored text origin after proportional flex shrink.
		left = int(math.Floor(float64(5*width) / float64(5+basis)))
	}
	for i, row := range exportWrapWords(rest, max(1, width-left)) {
		prefix := strings.Repeat(" ", left)
		if i == 0 {
			prefix = exportTruncate("  ⎿  ", left, false)
		}
		rows = append(rows, TerminalThemeRoleEscape(theme, "inactive", false)+prefix+displayText(row)+"\x1b[39m")
	}
	return rows
}
