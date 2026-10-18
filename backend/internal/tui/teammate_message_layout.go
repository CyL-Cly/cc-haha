package tui

import "math"

// The Text measure callback wraps at Yoga's fractional constraint and then
// counts visual lines at that same width. Bun.wrapAnsi floors its column
// argument, while painting uses the rounded Yoga edges. Measurement may
// therefore reserve a visually blank row beyond the painted text.
func terminalTeammateHeaderLines(parts []string, width int) []string {
	rows := terminalFooterFlexRow(parts, width)
	bases, total := make([]int, len(parts)), 0
	for index, part := range parts {
		for _, line := range exportWrapWords(part, width) {
			bases[index] = max(bases[index], exportWidth(line))
		}
		total += bases[index]
	}
	height := 0
	for index, part := range parts {
		plain := displayText(part)
		if plain == "" {
			continue
		}
		allocated := float64(bases[index])
		if total > width {
			allocated *= float64(width) / float64(total)
		}
		measured := 0
		for _, line := range exportWrapWords(plain, max(1, int(math.Floor(allocated)))) {
			n := exportWidth(line)
			if n == 0 || allocated <= 0 {
				measured++
			} else {
				measured += int(math.Ceil(float64(n) / allocated))
			}
		}
		height = max(height, measured)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}
