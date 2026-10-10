package contextusage

import "math"

func grid(categories []Category, window float64, terminalWidth int) [][]Square {
	width, height := 10, 10
	narrow := terminalWidth != 0 && terminalWidth < 80
	if window >= 1000000 {
		width = 20
	} else if narrow {
		height = 5
	}
	if narrow {
		width = 5
	}
	limit := width * height
	rows := []Square{}
	reserved := []Square{}
	free := Category{Name: "Free space", Color: "promptBorder"}
	makeSquares := func(cat Category) []Square {
		// Source rounds the multiply before subtracting wholeSquares. The
		// explicit conversion prevents Go from fusing multiply/subtract.
		exact := float64(cat.Tokens / window * float64(limit))
		count := math.Max(1, round(exact))
		if cat.Name == "Free space" {
			count = round(exact)
		}
		whole := math.Floor(exact)
		fraction := exact - whole
		result := []Square{}
		for i := 0; i < int(count) && i < limit; i++ {
			fullness := 1.0
			if float64(i) == whole && fraction > 0 {
				fullness = fraction
			}
			result = append(result, Square{cat.Color, true, cat.Name, cat.Tokens, round(cat.Tokens / window * 100), fullness})
		}
		return result
	}
	for _, cat := range categories {
		if cat.Deferred {
			continue
		}
		switch cat.Name {
		case "Free space":
			free = cat
		case "Autocompact buffer", "Compact buffer":
			reserved = makeSquares(cat)
		default:
			for _, square := range makeSquares(cat) {
				if len(rows) < limit {
					rows = append(rows, square)
				}
			}
		}
	}
	for len(rows) < limit-len(reserved) {
		rows = append(rows, Square{"promptBorder", true, "Free space", free.Tokens, round(free.Tokens / window * 100), 1})
	}
	for _, square := range reserved {
		if len(rows) < limit {
			rows = append(rows, square)
		}
	}
	result := [][]Square{}
	for i := 0; i < height; i++ {
		start, end := i*width, (i+1)*width
		if start > len(rows) {
			start = len(rows)
		}
		if end > len(rows) {
			end = len(rows)
		}
		result = append(result, rows[start:end])
	}
	return result
}
