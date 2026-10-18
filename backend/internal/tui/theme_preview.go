package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

type TerminalThemeSpan struct {
	Color           string `json:"color"`
	BackgroundColor string `json:"backgroundColor,omitempty"`
	Dim             bool   `json:"dim"`
	Text            string `json:"text"`
}

// The normal frozen distribution resolves color-diff-napi to its shipped
// stub. Its constructor exists, getSyntaxTheme reports the raw theme, and
// render returns null; the real StructuredDiffFallback owns the preview.
func terminalShippedColorDiffRender(theme string, width int) []string { return nil }

// ThemePicker's fixed demonstration contains ASCII only. Preserve Source
// wrapText's trim:false/hard:true word wrapping, including leading spaces;
// Cursor's input-specific whitespace reconstruction does not apply here.
func terminalThemeDemoWrap(text string, columns int) []string {
	columns = max(1, columns)
	rows := []string{""}
	for index, word := range strings.Split(text, " ") {
		length := len(rows[len(rows)-1])
		if index > 0 {
			if length >= columns {
				rows = append(rows, "")
				length = 0
			}
			rows[len(rows)-1] += " "
			length++
		}
		if len(word) > columns {
			thisBreaks := 1 + (len(word)-(columns-length)-1)/columns
			nextBreaks := (len(word) - 1) / columns
			if nextBreaks < thisBreaks {
				rows = append(rows, "")
			}
			for _, char := range word {
				if len(rows[len(rows)-1]) >= columns {
					rows = append(rows, "")
				}
				rows[len(rows)-1] += string(char)
			}
			continue
		}
		if length+len(word) > columns && length > 0 && len(word) > 0 {
			rows = append(rows, "")
		}
		rows[len(rows)-1] += word
	}
	return rows
}

func TerminalThemePreviewStyledRows(theme string, width int) [][]TerminalThemeSpan {
	if lines := terminalShippedColorDiffRender(theme, width); lines != nil {
		return nil
	}
	width = max(1, width)
	rows := [][]TerminalThemeSpan{}
	standard := func(number int, code string) {
		for index, text := range terminalThemeDemoWrap(code, max(1, width-5)) {
			numberText := "   "
			if index == 0 {
				numberText = fmt.Sprintf("%2d ", number)
			}
			padding := strings.Repeat(" ", max(0, width-len(numberText)-1-uniseg.StringWidth(text)))
			rows = append(rows, []TerminalThemeSpan{{Color: "text", Dim: true, Text: numberText}, {Color: "text", Dim: true, Text: " "}, {Color: "text", Text: text}, {Color: "text", Text: padding}})
		}
	}
	changed := func(sigil string, word string, bg string, wordBg string) {
		parts := []TerminalThemeSpan{{Color: "text", BackgroundColor: bg, Text: `  console.log("Hello, `}, {Color: "text", BackgroundColor: wordBg, Text: word}, {Color: "text", BackgroundColor: bg, Text: `!");`}}
		available := max(1, width-4)
		wrapped := [][]TerminalThemeSpan{}
		current := []TerminalThemeSpan{}
		cells := 0
		for _, part := range parts {
			for index, text := range terminalThemeDemoWrap(part.Text, available) {
				if text == "" {
					continue
				}
				length := uniseg.StringWidth(text)
				if index > 0 || cells+length > available {
					if len(current) > 0 {
						wrapped = append(wrapped, current)
						current = nil
						cells = 0
					}
				}
				span := part
				span.Text = text
				current = append(current, span)
				cells += length
			}
		}
		if len(current) > 0 {
			wrapped = append(wrapped, current)
		}
		for index, content := range wrapped {
			numberText := "   "
			if index == 0 {
				numberText = " 2 "
			}
			row := []TerminalThemeSpan{{Color: "text", BackgroundColor: bg, Text: numberText}, {Color: "text", BackgroundColor: bg, Text: sigil}}
			row = append(row, content...)
			used := len(numberText) + 1
			for _, span := range content {
				used += uniseg.StringWidth(span.Text)
			}
			row = append(row, TerminalThemeSpan{Color: "text", BackgroundColor: bg, Text: strings.Repeat(" ", max(0, width-used))})
			rows = append(rows, row)
		}
	}
	standard(1, "function greet() {")
	changed("-", "World", "diffRemoved", "diffRemovedWord")
	changed("+", "Claude", "diffAdded", "diffAddedWord")
	standard(3, "}")
	return rows
}

func TerminalThemeRole(theme, role string) string {
	values, ok := terminalThemeRoles[theme]
	if !ok {
		values = terminalThemeRoles["dark"]
	}
	if value := values[role]; value != "" {
		return value
	}
	footer, ok := terminalFooterThemeRoles[theme]
	if !ok {
		footer = terminalFooterThemeRoles["dark"]
	}
	return footer[role]
}

func TerminalThemeRoleEscape(theme, role string, background bool) string {
	return terminalThemeColorEscape(TerminalThemeRole(theme, role), background)
}

func terminalThemeColorEscape(value string, background bool) string {
	base := 30
	if background {
		base = 40
	}
	if strings.HasPrefix(value, "ansi:") {
		name := strings.TrimPrefix(value, "ansi:")
		bright := strings.HasSuffix(name, "Bright")
		name = strings.TrimSuffix(name, "Bright")
		index := map[string]int{"black": 0, "red": 1, "green": 2, "yellow": 3, "blue": 4, "magenta": 5, "cyan": 6, "white": 7}[name]
		if bright {
			base += 60
		}
		return "\x1b[" + strconv.Itoa(base+index) + "m"
	}
	var r, g, b int
	if _, err := fmt.Sscanf(value, "rgb(%d,%d,%d)", &r, &g, &b); err != nil {
		return ""
	}
	code := 38
	if background {
		code = 48
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", code, r, g, b)
}

func TerminalThemePreviewLines(theme string, width int) []string {
	lines := []string{}
	for _, row := range TerminalThemePreviewStyledRows(theme, width) {
		var line strings.Builder
		for _, span := range row {
			line.WriteString("\x1b[0m")
			line.WriteString(TerminalThemeRoleEscape(theme, span.Color, false))
			if span.BackgroundColor != "" {
				line.WriteString(TerminalThemeRoleEscape(theme, span.BackgroundColor, true))
			}
			if span.Dim {
				line.WriteString("\x1b[2m")
			}
			line.WriteString(span.Text)
		}
		line.WriteString("\x1b[0m")
		lines = append(lines, line.String())
	}
	return lines
}

func (p *localPanelState) terminalThemePickerLines(width, height int, shortcuts ...string) []string {
	theme := p.themePreview
	if theme == "" && len(p.props.Rows) > 0 {
		theme = p.props.Rows[min(max(p.selected, 0), len(p.props.Rows)-1)].ID
	}
	lines := []string{TerminalThemeRoleEscape(theme, "permission", false) + "\x1b[1mTheme\x1b[0m", "Choose the text style that looks best with your terminal", ""}
	for i, row := range p.props.Rows {
		prefix := "  "
		if i == p.selected {
			prefix = "> "
		}
		lines = append(lines, prefix+row.Label)
	}
	lines = append(lines, "")
	border := TerminalThemeRoleEscape(theme, "subtle", false) + strings.Repeat("╌", max(1, width)) + "\x1b[0m"
	lines = append(lines, border)
	lines = append(lines, TerminalThemePreviewLines(theme, width)...)
	lines = append(lines, border)
	meta := presentationObject(p.props.Presentation)
	if p.themeSyntaxTheme != theme {
		p.themeSyntaxTheme = theme
		p.themeSyntaxAvailable = meta.String("colorModuleUnavailableReason") != "env" && meta.String("currentColorModuleUnavailableReason") != "env"
	}
	shortcut := "ctrl+t"
	if len(shortcuts) > 0 && shortcuts[0] != "" {
		shortcut = shortcuts[0]
	}
	label := "Syntax theme: " + theme + " (" + shortcut + " to disable)"
	if meta.String("colorModuleUnavailableReason") == "env" {
		label = "Syntax highlighting disabled (via CLAUDE_CODE_SYNTAX_HIGHLIGHT=" + meta.String("syntaxEnvironment") + ")"
	} else if string(meta.Get("syntaxHighlightingDisabled")) == "true" {
		label = "Syntax highlighting disabled (" + shortcut + " to enable)"
	} else if !p.themeSyntaxAvailable {
		label = "Syntax highlighting enabled (" + shortcut + " to disable)"
	}
	lines = append(lines, " "+label, "Enter select · Esc cancel")
	return lines[:min(len(lines), max(1, height))]
}
