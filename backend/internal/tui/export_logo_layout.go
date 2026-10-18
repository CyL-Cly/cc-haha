package tui

import (
	"github.com/rivo/uniseg"
	"math"
	"strings"
)

// Ink's wrap mode preserves separator spaces across soft/hard boundaries.
func exportWrapWords(text string, width int) []string {
	return exportWrapAt(text, float64(max(1, width)))
}

func exportWrapAt(text string, width float64) []string {
	width = math.Max(1, width)
	rows := []string{""}
	column := float64(0)
	newLine := func() { rows = append(rows, ""); column = 0 }
	for lineIndex, paragraph := range strings.Split(displayText(text), "\n") {
		if lineIndex > 0 {
			newLine()
		}
		words := strings.Split(paragraph, " ")
		for index, word := range words {
			wordWidth := float64(exportWidth(word))
			if wordWidth > width {
				// Ink hard-wrap chooses the current row unless starting the
				// whole word on the next row avoids a further line break.
				remaining := width - column
				breaksHere := 1 + math.Floor((wordWidth-remaining-1)/width)
				breaksNext := math.Floor((wordWidth - 1) / width)
				if breaksNext < breaksHere {
					newLine()
				}
			} else if wordWidth > 0 && column > 0 && column+wordWidth > width {
				newLine()
			}
			g := uniseg.NewGraphemes(word)
			for g.Next() {
				cluster := g.Str()
				cells := float64(uniseg.StringWidth(cluster))
				if column > 0 && column+cells > width {
					newLine()
				}
				rows[len(rows)-1] += cluster
				column += cells
			}
			if index < len(words)-1 {
				if column >= width {
					newLine()
				}
				rows[len(rows)-1] += " "
				column++
			}
		}
	}
	return rows
}

func exportWidth(text string) int { return uniseg.StringWidth(displayText(text)) }
func exportTruncate(text string, width int, ellipsis bool) string {
	text = displayText(text)
	if exportWidth(text) <= width {
		return text
	}
	limit := max(0, width)
	if ellipsis {
		limit = max(0, width-1)
	}
	var out strings.Builder
	g := uniseg.NewGraphemes(text)
	used := 0
	for g.Next() {
		cluster := g.Str()
		n := uniseg.StringWidth(cluster)
		if used+n > limit {
			break
		}
		out.WriteString(cluster)
		used += n
	}
	if ellipsis && width > 0 {
		out.WriteString("…")
	}
	return out.String()
}
func exportCenter(text string, width int) string {
	text = displayText(text)
	padding := max(0, width-exportWidth(text))
	left := (padding + 1) / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
}
func exportCenterText(text string, width int) string {
	text = displayText(text)
	padding := max(0, width-exportWidth(text))
	left := padding / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
}
func exportCenterArt(text string, width int) string {
	return exportCenter(exportPad(text, 9), width)
}
func exportPad(text string, width int) string {
	return displayText(text) + strings.Repeat(" ", max(0, width-exportWidth(text)))
}
func exportPath(path string, width int) string {
	path = displayText(path)
	if exportWidth(path) <= width {
		return path
	}
	parts := strings.Split(path, "/")
	first, last := parts[0], parts[len(parts)-1]
	if len(parts) == 1 {
		return exportTruncate(path, width, true)
	}
	if first == "" && 2+exportWidth(last) >= width {
		return "/" + exportTruncate(last, max(1, width-1), true)
	}
	if first != "" && 3+exportWidth(last) >= width {
		return "…/" + exportTruncate(last, max(1, width-2), true)
	}
	if len(parts) == 2 {
		return exportTruncate(first, width-2-exportWidth(last), false) + "…/" + last
	}
	available := width - exportWidth(first) - exportWidth(last) - 3
	if available <= 0 {
		return exportTruncate(first, max(0, width-exportWidth(last)-3), false) + "/…/" + last
	}
	middle := []string{}
	for i := len(parts) - 2; i > 0; i-- {
		part := parts[i]
		if part != "" && exportWidth(part)+1 <= available {
			middle = append([]string{part}, middle...)
			available -= exportWidth(part) + 1
		} else {
			break
		}
	}
	if len(middle) == 0 {
		return first + "/…/" + last
	}
	return first + "/…/" + strings.Join(middle, "/") + "/" + last
}
func exportArt(terminal string) []string {
	if terminal == "Apple_Terminal" {
		return []string{"▗ ▗   ▖ ▖", "         ", "  ▘▘ ▝▝  "}
	}
	return []string{" ▐▛███▜▌", "▝▜█████▛▘", "  ▘▘ ▝▝  "}
}
func renderExportLogo(v ConversationExportPresentation) []string {
	columns := v.Columns
	if columns <= 0 {
		columns = 80
	}
	cwdAvailable := 50
	if v.Agent != "" {
		cwdAvailable -= 1 + exportWidth(v.Agent) + 3
	}
	cwd := exportPath(v.WorkingDirectory, max(10, cwdAvailable))
	cwdLine := cwd
	if v.Agent != "" {
		cwdLine = "@" + displayText(v.Agent) + " · " + cwd
	}
	model := exportTruncate(v.Model, 30, true)
	modelLine := model + " · " + displayText(v.BillingType)
	if v.Organization != "" {
		modelLine += " · " + displayText(v.Organization)
	}
	art := exportArt(v.Terminal)
	if !v.FullLogo {
		width := max(columns-15, 20)
		model := displayText(v.Model)
		modelRows := []string{}
		if exportWidth(model)+3+exportWidth(v.BillingType) > width {
			modelRows = []string{exportTruncate(model, width, true), exportTruncate(v.BillingType, width, true)}
		} else {
			modelRows = []string{exportTruncate(model, max(width-exportWidth(v.BillingType)-3, 10), true) + " · " + displayText(v.BillingType)}
		}
		cwdWidth := width
		if v.Agent != "" {
			cwdWidth -= 1 + exportWidth(v.Agent) + 3
		}
		cwd = exportPath(v.WorkingDirectory, max(cwdWidth, 10))
		if v.Agent != "" {
			cwd = "@" + displayText(v.Agent) + " · " + cwd
		}
		right := append([]string{"Claude Code v" + exportTruncate(v.Version, max(width-13, 6), true)}, modelRows...)
		right = append(right, cwd)
		if v.GuestPasses {
			text := "[✻] [✻] [✻] · 3 guest passes at /passes"
			if v.GuestPassReward {
				text = "[✻] [✻] [✻] · " + v.GuestPassSubtitle + " · /passes"
			}
			right = append(right, text)
		} else if v.OverageCreditUpsell {
			right = append(right, exportTruncate(v.OverageCreditAmount+" in extra usage", width, true), exportTruncate("On us. Works on third-party apps · /extra-usage", width, true))
		}
		return exportCondensedLayout(art, right, columns)
	}
	if columns < 70 {
		available := columns - 4
		welcome := v.Welcome
		if exportWidth(welcome) > available {
			welcome = "Welcome back!"
		}
		cwdWidth := available
		if v.Agent != "" {
			cwdWidth -= 1 + exportWidth(v.Agent) + 3
		}
		cwd := exportPath(v.WorkingDirectory, max(10, cwdWidth))
		if v.Agent != "" {
			cwd = "@" + displayText(v.Agent) + " · " + cwd
		}
		content := []string{"", exportCenterText(welcome, available), ""}
		for _, line := range art {
			content = append(content, exportCenterArt(line, available))
		}
		content = append(content, "", exportCenterText(model, available), exportCenterText(v.BillingType, available), exportCenterText(cwd, available), "")
		title := " Claude Code "
		top := "╭─" + title + strings.Repeat("─", max(0, columns-3-exportWidth(title))) + "╮"
		out := []string{top}
		for _, line := range content {
			out = append(out, "│ "+exportPad(line, available)+" │")
		}
		return append(out, "╰"+strings.Repeat("─", max(0, columns-2))+"╯")
	}
	leftWidth := min(50, max(20, max(exportWidth(v.Welcome), max(exportWidth(cwdLine), exportWidth(modelLine))))+4)
	rightWidth := columns - 7 - leftWidth
	intrinsicRight, _ := exportActivityWidth(v)
	if v.Onboarding {
		intrinsicRight = max(intrinsicRight, exportWidth("Tips for getting started"))
		for _, tip := range v.Tips {
			intrinsicRight = max(intrinsicRight, exportWidth(tip))
		}
	} else if v.GuestPasses {
		intrinsicRight = max(intrinsicRight, 48)
	} else if v.OverageCreditUpsell {
		intrinsicRight = max(intrinsicRight, exportWidth("On us. Works on third-party apps · /extra-usage"))
		intrinsicRight = max(intrinsicRight, exportWidth(v.OverageCreditAmount+" in extra usage"))
	} else {
		intrinsicRight = max(intrinsicRight, exportWidth("What's new"))
		if len(v.ReleaseNotes) == 0 {
			intrinsicRight = max(intrinsicRight, exportWidth("Check the Claude Code changelog for updates"))
		}
		for _, note := range v.ReleaseNotes {
			intrinsicRight = max(intrinsicRight, exportWidth(note))
		}
		if len(v.ReleaseNotes) > 0 {
			intrinsicRight = max(intrinsicRight, exportWidth("/release-notes for more"))
		}
	}
	rightWidth = min(rightWidth, intrinsicRight)
	columns = leftWidth + rightWidth + 7
	// FeedColumn's actual width is the maximum intrinsic feed width capped by
	// the layout's rightWidth; the logo box expands to the real terminal width.
	right := []string{}
	if v.Onboarding {
		right = append(right, "Tips for getting started")
		for _, tip := range v.Tips {
			right = append(right, exportTruncate(tip, max(10, rightWidth), true))
		}
	} else {
		right = append(right, exportActivityRows(v, rightWidth)...)
	}
	right = append(right, strings.Repeat("─", max(0, rightWidth)))
	if v.Onboarding {
		right = append(right, exportActivityRows(v, rightWidth)...)
	} else if v.GuestPasses {
		right = append(right, "3 guest passes", "", "[✻] [✻] [✻]", "")
		right = append(right, exportWrapWords(v.GuestPassSubtitle, rightWidth)...)
		right = append(right, exportTruncate("/passes", rightWidth, true))
	} else if v.OverageCreditUpsell {
		right = append(right, v.OverageCreditAmount+" in extra usage")
		right = append(right, exportWrapWords("On us. Works on third-party apps · /extra-usage", rightWidth)...)
	} else {
		right = append(right, "What's new")
		if len(v.ReleaseNotes) == 0 {
			right = append(right, exportTruncate("Check the Claude Code changelog for updates", rightWidth, true))
		} else {
			for _, note := range v.ReleaseNotes {
				right = append(right, exportTruncate(note, max(10, rightWidth), true))
			}
			right = append(right, exportTruncate("/release-notes for more", rightWidth, true))
		}
	}
	height := max(9, len(right))
	left := make([]string, height)
	for i := range left {
		left[i] = ""
	}
	left[1] = exportCenter(v.Welcome, leftWidth)
	artStart := 3 + (height-8)/2
	for i, line := range art {
		left[artStart+i] = exportCenterArt(line, leftWidth)
	}
	modelRows := wrap(modelLine, leftWidth)
	cwdRows := wrap(cwdLine, leftWidth)
	bottom := height - len(modelRows) - len(cwdRows)
	groupWidth := max(exportWidth(modelLine), exportWidth(cwdLine))
	for i, line := range append(modelRows, cwdRows...) {
		left[bottom+i] = exportCenter(exportCenterText(line, min(leftWidth, groupWidth)), leftWidth)
	}
	title := " Claude Code v" + displayText(v.Version) + " "
	top := "╭───" + title + strings.Repeat("─", max(0, columns-5-exportWidth(title))) + "╮"
	out := []string{top}
	for i := 0; i < height; i++ {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		out = append(out, "│ "+exportPad(left[i], leftWidth)+" │ "+exportPad(r, max(0, rightWidth))+" │")
	}
	return append(out, "╰"+strings.Repeat("─", max(0, columns-2))+"╯")
}
