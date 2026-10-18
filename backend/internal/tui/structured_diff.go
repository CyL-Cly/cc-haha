package tui

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"github.com/rivo/uniseg"
)

type terminalDiffLine struct {
	code, kind, matched string
	number              int
	paired              bool
}

func terminalStructuredDiffWrap(text string, columns int) []string {
	columns = max(1, columns)
	rows := []string{""}
	for index, word := range strings.Split(text, " ") {
		length := uniseg.StringWidth(rows[len(rows)-1])
		if index > 0 {
			if length >= columns {
				rows = append(rows, "")
				length = 0
			}
			rows[len(rows)-1] += " "
			length++
		}
		wordWidth := uniseg.StringWidth(word)
		if wordWidth > columns {
			thisBreaks := 1 + int(math.Floor(float64(wordWidth-(columns-length)-1)/float64(columns)))
			nextBreaks := int(math.Floor(float64(wordWidth-1) / float64(columns)))
			if nextBreaks < thisBreaks {
				rows = append(rows, "")
			}
			visible := uniseg.StringWidth(rows[len(rows)-1])
			clusters := []string{}
			g := uniseg.NewGraphemes(word)
			for g.Next() {
				clusters = append(clusters, g.Str())
			}
			for i, cluster := range clusters {
				cells := uniseg.StringWidth(cluster)
				if visible+cells <= columns {
					rows[len(rows)-1] += cluster
				} else {
					rows = append(rows, cluster)
					visible = 0
				}
				visible += cells
				if visible == columns && i < len(clusters)-1 {
					rows = append(rows, "")
					visible = 0
				}
			}
			if visible == 0 && len(rows) > 1 && rows[len(rows)-1] != "" {
				rows[len(rows)-2] += rows[len(rows)-1]
				rows = rows[:len(rows)-1]
			}
			continue
		}
		if length+wordWidth > columns && length > 0 && wordWidth > 0 {
			rows = append(rows, "")
		}
		rows[len(rows)-1] += word
	}
	return rows
}

// TerminalStructuredDiffRows is the shipped color-diff stub's actual fallback:
// adjacent remove/add pairing, diffWordsWithSpace, 40% word-change threshold,
// original oldStart numbering and wrapText's word-preserving hard wrap.
func TerminalStructuredDiffRows(h terminaldiff.Hunk, width int, theme string) [][]TerminalThemeSpan {
	lines := []terminalDiffLine{}
	for _, text := range h.Lines {
		kind, code := "nochange", ""
		if len(text) > 0 {
			code = text[1:]
			if text[0] == '+' {
				kind = "add"
			} else if text[0] == '-' {
				kind = "remove"
			}
		}
		lines = append(lines, terminalDiffLine{code: code, kind: kind})
	}
	for i := 0; i < len(lines); {
		if lines[i].kind != "remove" {
			i++
			continue
		}
		j := i
		for j < len(lines) && lines[j].kind == "remove" {
			j++
		}
		k := j
		for k < len(lines) && lines[k].kind == "add" {
			k++
		}
		for offset := 0; offset < min(j-i, k-j); offset++ {
			lines[i+offset].paired = true
			lines[i+offset].matched = lines[j+offset].code
			lines[j+offset].paired = true
			lines[j+offset].matched = lines[i+offset].code
		}
		i = max(i+1, k)
	}
	number, maxNumber := h.OldStart, 0
	for i := 0; i < len(lines); i++ {
		lines[i].number = number
		maxNumber = max(maxNumber, number)
		if lines[i].kind != "remove" {
			number++
			continue
		}
		n := 0
		for i+1 < len(lines) && lines[i+1].kind == "remove" {
			i++
			n++
			number++
			lines[i].number = number
			maxNumber = max(maxNumber, number)
		}
		number -= n
	}
	width = max(1, width)
	gutter := len(fmt.Sprint(maxNumber)) + 1
	out := [][]TerminalThemeSpan{}
	for _, line := range lines {
		bg, sigil := "", " "
		if line.kind == "add" {
			bg, sigil = "diffAdded", "+"
		} else if line.kind == "remove" {
			bg, sigil = "diffRemoved", "-"
		}
		parts := []builtinwrite.WordChange{}
		word := false
		if line.paired {
			old, next := line.code, line.matched
			if line.kind == "add" {
				old, next = next, old
			}
			parts = builtinwrite.WordDiff(old, next)
			changed := 0
			for _, p := range parts {
				if p.Added || p.Removed {
					changed += len(utf16.Encode([]rune(p.Value)))
				}
			}
			total := len(utf16.Encode([]rune(old))) + len(utf16.Encode([]rune(next)))
			word = total == 0 || float64(changed)/float64(total) <= .4
		}
		contents := [][]TerminalThemeSpan{}
		if word {
			available := max(1, width-gutter-2)
			current := []TerminalThemeSpan{}
			cells := 0
			for _, part := range parts {
				if line.kind == "add" && part.Removed || line.kind == "remove" && part.Added {
					continue
				}
				wordBG := ""
				if line.kind == "add" && part.Added {
					wordBG = "diffAddedWord"
				} else if line.kind == "remove" && part.Removed {
					wordBG = "diffRemovedWord"
				}
				for i, text := range terminalStructuredDiffWrap(part.Value, available) {
					if text == "" {
						continue
					}
					size := uniseg.StringWidth(text)
					if i > 0 || cells+size > available {
						if len(current) > 0 {
							contents = append(contents, current)
							current = nil
							cells = 0
						}
					}
					current = append(current, TerminalThemeSpan{Color: "text", BackgroundColor: wordBG, Text: text})
					cells += size
				}
			}
			if len(current) > 0 {
				contents = append(contents, current)
			}
		} else {
			for _, text := range terminalStructuredDiffWrap(line.code, max(1, width-gutter-3)) {
				contents = append(contents, []TerminalThemeSpan{{Color: "text", Text: text}})
			}
		}
		for index, content := range contents {
			num := strings.Repeat(" ", gutter)
			if index == 0 {
				num = fmt.Sprintf("%*d", gutter, line.number)
			}
			num += " "
			row := []TerminalThemeSpan{{Color: "text", BackgroundColor: bg, Dim: line.kind == "nochange", Text: num}, {Color: "text", BackgroundColor: bg, Dim: line.kind == "nochange", Text: sigil}}
			used := len(num) + 1
			for _, span := range content {
				if span.BackgroundColor == "" {
					span.BackgroundColor = bg
				}
				used += uniseg.StringWidth(span.Text)
				row = append(row, span)
			}
			row = append(row, TerminalThemeSpan{Color: "text", BackgroundColor: bg, Text: strings.Repeat(" ", max(0, width-used))})
			out = append(out, row)
		}
	}
	return out
}
func terminalStructuredDiffStrings(h terminaldiff.Hunk, width int, theme string) []string {
	out := []string{}
	for _, row := range TerminalStructuredDiffRows(h, width, theme) {
		var line strings.Builder
		for _, span := range row {
			line.WriteString(TerminalThemeRoleEscape(theme, span.Color, false))
			if span.BackgroundColor != "" {
				line.WriteString(TerminalThemeRoleEscape(theme, span.BackgroundColor, true))
			}
			if span.Dim {
				line.WriteString("\x1b[2m")
			}
			line.WriteString(displayText(span.Text))
			line.WriteString("\x1b[0m")
		}
		out = append(out, line.String())
	}
	return out
}
