package contextusage

// PresentationGrid shares the analyser's grid rounding and category ordering
// while allowing the real terminal's width to select the Source layout.
func PresentationGrid(data Data, terminalWidth int) [][]Square {
	return grid(data.Categories, data.RawMax, terminalWidth)
}
