package tui

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"regexp"
	"strings"
)

const terminalJSWhitespace = `[\t\n\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

func terminalExtractTag(text, tag string) (string, bool) {
	if protocol.TrimJavaScriptString(text) == "" {
		return "", false
	}
	name := regexp.QuoteMeta(tag)
	open := `<` + name + `(?:` + terminalJSWhitespace + `+[^>]*)?>`
	close := `</` + name + `>`
	pattern := regexp.MustCompile(`(?is)` + open + `(.*?)` + close)
	opening := regexp.MustCompile(`(?is)` + open)
	closing := regexp.MustCompile(`(?is)` + close)
	last := 0
	for _, match := range pattern.FindAllStringSubmatchIndex(text, -1) {
		before := text[last:match[0]]
		depth := len(opening.FindAllStringIndex(before, -1)) - len(closing.FindAllStringIndex(before, -1))
		value := text[match[2]:match[3]]
		if depth == 0 && value != "" {
			return value, true
		}
		last = match[1]
	}
	return "", false
}

// Local command tags are presentation metadata in the original UserTextMessage.
// These projections never change the retained canonical user content.
func terminalLocalUserMessageLines(text string, width int, theme string) ([]string, bool) {
	if protocol.TrimJavaScriptString(text) == "(no content)" || strings.Contains(text, "<local-command-caveat>") {
		return nil, true
	}
	if _, ok := terminalExtractTag(text, "tick"); ok {
		return nil, true
	}
	if strings.HasPrefix(text, "<local-command-stdout") || strings.HasPrefix(text, "<local-command-stderr") {
		stdout, hasOut := terminalExtractTag(text, "local-command-stdout")
		stderr, hasErr := terminalExtractTag(text, "local-command-stderr")
		if !hasOut && !hasErr {
			return []string{"  ⎿ \u00a0(no content)"}, true
		}
		rows := []string{}
		for _, value := range []string{stdout, stderr} {
			value = protocol.TrimJavaScriptString(value)
			if value == "" {
				continue
			}
			if strings.HasPrefix(value, "◇ ") || strings.HasPrefix(value, "◆ ") {
				rows = append(rows, terminalCloudLocalOutputLines(value, width, theme)...)
				continue
			}
			left, contentWidth := terminalLocalOutputColumns(value, width)
			for index, row := range TerminalMarkdownRows(value, contentWidth, width, theme) {
				prefix := strings.Repeat(" ", left)
				if index == 0 {
					prefix = exportTruncate("  ⎿  ", left, false)
				}
				rows = append(rows, prefix+row)
			}
		}
		return rows, true
	}
	if strings.Contains(text, "<command-message>") {
		message, ok := terminalExtractTag(text, "command-message")
		if !ok {
			return nil, true
		}
		args, _ := terminalExtractTag(text, "command-args")
		body := "/" + message
		if args != "" {
			body += " " + args
		}
		if skill, _ := terminalExtractTag(text, "skill-format"); skill == "true" {
			body = "Skill(" + message + ")"
		}
		return exportWrapWords("❯ "+body, max(1, width-1)), true
	}
	return nil, false
}

// Both children of IndentedContent shrink from their measured flex bases.
// Yoga rounds the column box's position; its measured prefix text can overlap
// that box by one cell. Painting uses the rounded right-box width.
func terminalLocalOutputColumns(value string, width int) (int, int) {
	width = max(1, width)
	basis := 0
	for _, line := range terminalMarkdownRows(value, width, width, "dark") {
		basis = max(basis, exportWidth(line))
	}
	left := 5
	if basis+left > width {
		left = int(math.Floor(float64(5*width)/float64(5+basis) + .5))
	}
	return left, max(1, width-left)
}
