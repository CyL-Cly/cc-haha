package tui

import (
	"math"
	"strings"
)

func exportMeasuredText(text string, width float64) (int, int) {
	rows := strings.Split(displayText(text), "\n")
	natural := 0
	for _, row := range rows {
		natural = max(natural, exportWidth(row))
	}
	if float64(natural) > width && width >= 1 {
		rows = exportWrapAt(text, width)
	}
	measured, height := 0, 0
	for _, row := range rows {
		cells := exportWidth(row)
		measured = max(measured, cells)
		if cells == 0 || width < 1 {
			height++
		} else {
			height += int(math.Ceil(float64(cells) / width))
		}
	}
	return measured, height
}

// The original condensed flex row shrinks both columns by their intrinsic
// basis. Ink measures text at the fractional width before Yoga rounds boxes,
// then renders at the rounded text width. Those distinct widths matter for a
// wrapped guest-pass line: Clawd can measure taller than its rendered glyphs.
func exportCondensedLayout(art, texts []string, columns int) []string {
	rightBasis := 0
	for _, text := range texts {
		width, _ := exportMeasuredText(text, float64(max(1, columns-2)))
		rightBasis = max(rightBasis, width)
	}
	leftWidth, rightWidth := float64(9), float64(rightBasis)
	if total := leftWidth + rightWidth; total+2 > float64(columns) {
		leftWidth *= float64(max(0, columns-2)) / total
		rightWidth *= float64(max(0, columns-2)) / total
	}
	rightStart := int(math.Round(leftWidth + 2))
	leftRenderWidth := max(1, int(math.Ceil(leftWidth)))
	rightRenderWidth := max(1, int(math.Ceil(leftWidth+2+rightWidth)-math.Floor(leftWidth+2)))
	project := func(lines []string, width float64, renderWidth int) []string {
		rows := []string{}
		for _, line := range lines {
			_, height := exportMeasuredText(line, width)
			visible := exportWrapWords(line, renderWidth)
			rows = append(rows, visible...)
			for len(visible) < height {
				rows = append(rows, "")
				height--
			}
		}
		return rows
	}
	left, right := project(art, leftWidth, leftRenderWidth), project(texts, rightWidth, rightRenderWidth)
	height := max(len(left), len(right))
	leftOffset, rightOffset := int(math.Round(float64(height-len(left))/2)), int(math.Round(float64(height-len(right))/2))
	rows := []string{}
	for i := 0; i < height; i++ {
		a, b := "", ""
		if j := i - leftOffset; j >= 0 && j < len(left) {
			a = left[j]
		}
		if j := i - rightOffset; j >= 0 && j < len(right) {
			b = right[j]
		}
		rows = append(rows, strings.TrimRight(exportPad(a, rightStart)+b, " "))
	}
	return rows
}
