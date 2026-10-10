package tui

import "strings"

// The same centered visible window as PromptInputFooterSuggestions. Selection
// remains in State; rendering neither sorts nor mutates the shared command list.
func completionWindow(count, selected, height int, overlay bool) (int, int) {
	visible := min(6, max(1, height-3))
	if overlay {
		visible = 5
	}
	start := max(0, min(selected-visible/2, count-visible))
	return start, min(start+visible, count)
}

func (s *State) completionLines(overlay bool) []string {
	start, end := completionWindow(len(s.Completions), s.Selected, s.Height, overlay)
	lines := []string{}
	for i := start; i < end; i++ {
		command := s.Completions[i]
		prefix := "  "
		if i == s.Selected {
			prefix = "> "
		}
		text := prefix + "/" + strings.TrimPrefix(command.Name, "/")
		if command.Description != "" {
			text += " · " + strings.Join(strings.Fields(command.Description), " ")
		}
		row := wrap(text, s.Width)
		if len(row) > 0 {
			lines = append(lines, row[0])
		}
	}
	return lines
}
