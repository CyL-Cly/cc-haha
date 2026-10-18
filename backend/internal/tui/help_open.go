package tui

import (
	"context"
	"math"
	"runtime"
	"strings"
	"time"
)

type TerminalShortcutHelp struct {
	Platform, Newline                     string
	FastModeAvailable, KeybindingsEnabled bool
}
type TerminalShortcutHelpSource interface {
	TerminalShortcutHelp(context.Context) (TerminalShortcutHelp, error)
}

func (s *State) shortcutHelpInput(value string) bool {
	if value == "?" {
		s.HelpOpen = !s.HelpOpen
		return true
	}
	s.HelpOpen = false
	return false
}

// These original Chat handlers call trackAndSetInput directly. They do not
// pass through PromptInput.onChange, which owns closing the shortcut help.
func (s *State) shortcutHelpTrackedInputKey(key Key) bool {
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Chat", "Help"}, s.bindings, s.pendingChord)
	if resolution.Type != "match" {
		return false
	}
	switch resolution.Action {
	case "chat:undo", "chat:newline", "chat:stash", "chat:externalEditor", "chat:imagePaste":
		return true
	}
	return false
}

func (s *State) handleShortcutHelpKey(key Key, now time.Time) (bool, []action) {
	if s.Transcript || s.Searching || len(s.Permissions) > 0 || s.LocalPanel != nil || s.ModelPicker != nil || s.WorktreeExit != nil {
		return false, nil
	}
	if key.Text == "?" && s.Editor.Text() == "" && (s.Preferences.EditorMode != "vim" || s.VimMode() == "INSERT") {
		s.shortcutHelpInput("?")
		return true, nil
	}
	if !s.HelpOpen {
		return false, nil
	}
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Chat", "Help"}, s.bindings, s.pendingChord)
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	}
	if key.Name == "esc" {
		s.pendingChord = nil
		s.HelpOpen = false
		return true, nil
	}
	if resolution.Type == "unbound" {
		s.pendingChord = nil
		return true, nil
	}
	if resolution.Type == "match" && resolution.Action == "help:dismiss" {
		s.pendingChord = nil
		s.HelpOpen = false
		return true, nil
	}
	if key.Name == "enter" || s.Editor.Text() == "" && (key.Name == "backspace" || key.Name == "delete") {
		s.HelpOpen = false
		return true, nil
	}
	if resolution.Type == "match" && resolution.Action == "chat:fastMode" && !s.ShortcutHelp.FastModeAvailable {
		return true, nil
	}
	if resolution.Type == "match" {
		switch resolution.Action {
		case "chat:modelPicker", "chat:fastMode", "chat:thinkingToggle", "chat:cycleMode":
			s.HelpOpen = false
		}
	}
	return false, nil
}

type terminalShortcutHelpLayout struct {
	padding, gap int
	role         string
}

func (s *State) shortcutHelpRows(layouts ...terminalShortcutHelpLayout) []string {
	layout := terminalShortcutHelpLayout{padding: 2, role: "inactive"}
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	columns := s.shortcutHelpColumns()
	three := columns[2]
	// Frozen TERMINAL_PANEL is disabled. The three original columns use
	// fixed bases 24 and 35; the final column measures its actual labels.
	widths := []int{24, 35, 0}
	for _, row := range three {
		widths[2] = max(widths[2], exportWidth(row))
	}
	return terminalShortcutHelpPaint(columns, widths, max(1, s.Width-2*layout.padding), s.previewTheme(), layout)
}

func terminalShortcutHelpPaint(columns [][]string, bases []int, available int, theme string, layouts ...terminalShortcutHelpLayout) []string {
	layout := terminalShortcutHelpLayout{padding: 2, role: "inactive"}
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	space := max(1, available-layout.gap*(len(columns)-1))
	// Yoga measures the auto-sized final column against the parent content
	// width before distributing shrink. The two explicit width bases retain
	// their configured sizes during that measurement.
	bases = append([]int(nil), bases...)
	bases[len(bases)-1] = min(bases[len(bases)-1], available)
	total := 0
	for _, basis := range bases {
		total += basis
	}
	origin := float64(0)
	grid := [][]*terminalStatusLineCell{}
	for index, labels := range columns {
		allocated := float64(bases[index])
		if total > space {
			allocated *= float64(space) / float64(total)
		}
		left := int(math.Floor(origin + 0.5))
		origin += allocated + float64(layout.gap)
		y := 0
		for _, label := range labels {
			first := exportWrapWords(label, max(1, int(math.Floor(allocated))))
			measured := 1
			for _, line := range first {
				measured = max(measured, exportWidth(line))
			}
			painted := terminalMarkdownWrap(terminalPillStyle(label, theme, layout.role, false, false), measured)
			for offset, line := range painted {
				for len(grid) <= y+offset {
					grid = append(grid, make([]*terminalStatusLineCell, available))
				}
				x := left
				for _, cell := range terminalStatusLineParse(line, false, "39") {
					width := exportWidth(cell.text)
					if x >= 0 && x+width <= available {
						copy := cell
						grid[y+offset][x] = &copy
						for j := 1; j < width; j++ {
							grid[y+offset][x+j] = &terminalStatusLineCell{text: "", style: cell.style}
						}
					}
					x += width
				}
			}
			y += len(first)
		}
	}
	rows := []string{}
	for _, line := range grid {
		end := len(line)
		for end > 0 && line[end-1] == nil {
			end--
		}
		var row strings.Builder
		row.WriteString(strings.Repeat(" ", layout.padding))
		for _, cell := range line[:end] {
			if cell == nil {
				row.WriteByte(' ')
			} else if cell.text != "" {
				row.WriteString("\x1b[0;" + cell.style.sgr() + "m" + cell.text + "\x1b[0m")
			}
		}
		rows = append(rows, row.String())
	}
	return rows
}

func (s *State) shortcutHelpColumns() [][]string {
	shortcut := func(action, context, fallback string) string {
		if value, ok := BindingDisplay(action, context, s.bindings); ok {
			return strings.ReplaceAll(value, "+", " + ")
		}
		return strings.ReplaceAll(fallback, "+", " + ")
	}
	one := []string{"! for bash mode", "/ for commands", "@ for file paths", "& for background", "/btw for side question"}
	newline := s.ShortcutHelp.Newline
	if newline == "" {
		newline = "backslash (\\) + return (⏎) for newline"
	}
	two := []string{"double tap esc to clear input", shortcut("chat:cycleMode", "Chat", "shift+tab") + " to auto-accept edits", shortcut("app:toggleTranscript", "Global", "ctrl+o") + " for verbose output", shortcut("app:toggleTodos", "Global", "ctrl+t") + " to toggle tasks", newline}
	three := []string{shortcut("chat:undo", "Chat", "ctrl+_") + " to undo"}
	platform := s.ShortcutHelp.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "windows" {
		three = append(three, "ctrl + z to suspend")
	}
	three = append(three, shortcut("chat:imagePaste", "Chat", "ctrl+v")+" to paste images", shortcut("chat:modelPicker", "Chat", "alt+p")+" to switch model")
	if s.ShortcutHelp.FastModeAvailable {
		three = append(three, shortcut("chat:fastMode", "Chat", "alt+o")+" to toggle fast mode")
	}
	three = append(three, shortcut("chat:stash", "Chat", "ctrl+s")+" to stash prompt", shortcut("chat:externalEditor", "Chat", "ctrl+g")+" to edit in $EDITOR")
	if s.ShortcutHelp.KeybindingsEnabled {
		three = append(three, "/keybindings to customize")
	}
	return [][]string{one, two, three}
}
