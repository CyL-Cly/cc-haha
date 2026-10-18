package tui

import "strings"

// TerminalPlanDisplay is PlanDisplay's plain static frame. The canonical
// result uses Ink's original 80-column wrap, independent of the live TTY width.
func TerminalPlanDisplay(path, content, editorName string) string {
	rows := []string{"Current Plan"}
	rows = append(rows, exportWrapWords(path, 80)...)
	rows = append(rows, "")
	rows = append(rows, exportWrapWords(content, 80)...)
	if editorName != "" {
		rows = append(rows, "")
		rows = append(rows, exportWrapWords(`"/plan open" to edit this plan in `+editorName, 80)...)
	}
	return strings.TrimRight(strings.Join(rows, "\n"), "\n")
}
