package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	extast "github.com/yuin/goldmark/extension/ast"
)

func (r terminalMarkdownRenderer) table(node *extast.Table, width, terminalWidth int) []string {
	contents := [][]string{}
	for row := node.FirstChild(); row != nil; row = row.NextSibling() {
		cells := []string{}
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, r.children(cell, 0))
		}
		contents = append(contents, cells)
	}
	if len(contents) == 0 || len(contents[0]) == 0 {
		return nil
	}
	count := len(contents[0])
	minimum, ideal := make([]int, count), make([]int, count)
	for col := 0; col < count; col++ {
		minimum[col], ideal[col] = 3, 3
		for _, row := range contents {
			if col >= len(row) {
				continue
			}
			plain := displayText(row[col])
			ideal[col] = max(ideal[col], exportWidth(plain))
			for _, word := range strings.FieldsFunc(plain, jsSpace) {
				minimum[col] = max(minimum[col], exportWidth(word))
			}
		}
	}
	available := max(terminalWidth-(1+count*3)-4, count*3)
	totalMin, totalIdeal := 0, 0
	for col := 0; col < count; col++ {
		totalMin += minimum[col]
		totalIdeal += ideal[col]
	}
	columns := append([]int(nil), ideal...)
	hard := false
	if totalIdeal > available && totalMin <= available {
		for col := 0; col < count; col++ {
			extra := 0
			if totalIdeal != totalMin {
				extra = int(math.Floor(float64(ideal[col]-minimum[col]) / float64(totalIdeal-totalMin) * float64(available-totalMin)))
			}
			columns[col] = minimum[col] + extra
		}
	} else if totalMin > available {
		hard = true
		for col := 0; col < count; col++ {
			columns[col] = max(3, int(math.Floor(float64(minimum[col])*float64(available)/float64(totalMin))))
		}
	}
	wrapCell := func(value string, column int) []string {
		return terminalMarkdownTableWrap(value, column, hard)
	}
	maxHeight := 1
	for _, row := range contents {
		for col, value := range row {
			maxHeight = max(maxHeight, len(wrapCell(value, columns[col])))
		}
	}
	border := func(left, mid, right string) string {
		value := left
		for col, column := range columns {
			value += strings.Repeat("─", column+2)
			if col+1 < count {
				value += mid
			} else {
				value += right
			}
		}
		return value
	}
	rows := []string{border("┌", "┬", "┐")}
	for index, row := range contents {
		wrapped, height := [][]string{}, 1
		for col, value := range row {
			cell := wrapCell(value, columns[col])
			wrapped = append(wrapped, cell)
			height = max(height, len(cell))
		}
		for y := 0; y < height; y++ {
			line := "│"
			for col, cell := range wrapped {
				value := ""
				position := y - (height-len(cell))/2
				if position >= 0 && position < len(cell) {
					value = cell[position]
				}
				align := extast.AlignLeft
				if col < len(node.Alignments) {
					align = node.Alignments[col]
				}
				if index == 0 {
					align = extast.AlignCenter
				}
				line += " " + terminalMarkdownPad(value, columns[col], align) + " │"
			}
			rows = append(rows, line)
		}
		if index+1 < len(contents) {
			rows = append(rows, border("├", "┼", "┤"))
		}
	}
	rows = append(rows, border("└", "┴", "┘"))
	if maxHeight > 4 || exportWidth(rows[0]) > terminalWidth-4 {
		rows = nil
		for index, row := range contents[1:] {
			if index > 0 {
				rows = append(rows, strings.Repeat("─", max(0, min(terminalWidth-1, 40))))
			}
			for col, value := range row {
				label := displayText(contents[0][col])
				if label == "" {
					label = fmt.Sprintf("Column %d", col+1)
				}
				value = strings.Join(strings.FieldsFunc(value, jsSpace), " ")
				firstWidth, remainingWidth := terminalWidth-exportWidth(label)-3, terminalWidth-3
				wrapped := terminalMarkdownTableWrap(value, max(firstWidth, 10), false)
				if len(wrapped) > 1 && remainingWidth > firstWidth {
					rest := []string{}
					for _, line := range wrapped[1:] {
						rest = append(rest, protocol.TrimJavaScriptString(line))
					}
					wrapped = append(wrapped[:1], terminalMarkdownTableWrap(strings.Join(rest, " "), remainingWidth, false)...)
				}
				rows = append(rows, "\x1b[1m"+label+":\x1b[22m "+wrapped[0])
				for _, line := range wrapped[1:] {
					if protocol.TrimJavaScriptString(line) != "" {
						rows = append(rows, "  "+line)
					}
				}
			}
		}
	}
	return terminalMarkdownWrap(strings.Join(rows, "\n"), width)
}

func terminalMarkdownPad(value string, width int, align extast.Alignment) string {
	padding := max(0, width-exportWidth(value))
	if align == extast.AlignCenter {
		return strings.Repeat(" ", padding/2) + value + strings.Repeat(" ", padding-padding/2)
	}
	if align == extast.AlignRight {
		return strings.Repeat(" ", padding) + value
	}
	return value + strings.Repeat(" ", padding)
}

func terminalMarkdownTableWrap(value string, width int, hard bool) []string {
	value = strings.TrimRightFunc(value, jsSpace)
	if value == "" {
		return []string{""}
	}
	// wrapAnsi uses word wrapping and enables hard splits only when the original
	// table's minimum widths no longer fit its available columns.
	rows := []string{}
	for _, line := range strings.Split(value, "\n") {
		wrapped := []string{line}
		if hard {
			wrapped = terminalMarkdownWrap(line, width)
		} else {
			wrapped = terminalMarkdownSoftWrap(line, width)
		}
		for _, result := range wrapped {
			if result != "" {
				rows = append(rows, result)
			}
		}
	}
	if len(rows) == 0 {
		return []string{""}
	}
	return rows
}

func terminalMarkdownSoftWrap(value string, width int) []string {
	// The measured words stay intact, including overlong single words. ANSI
	// styles are carried by the shared cell-offset wrapper after line geometry.
	plain := displayText(value)
	rows := []string{""}
	for i, word := range strings.Split(plain, " ") {
		length := exportWidth(rows[len(rows)-1])
		if i > 0 {
			if length >= max(1, width) {
				rows = append(rows, "")
				length = 0
			}
			rows[len(rows)-1] += " "
			length++
		}
		wordWidth := exportWidth(word)
		if length+wordWidth > max(1, width) && length > 0 && wordWidth > 0 {
			rows = append(rows, "")
		}
		rows[len(rows)-1] += word
	}
	return terminalMarkdownApplyStyles(value, rows)
}
