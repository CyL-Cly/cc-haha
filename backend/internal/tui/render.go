package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// displayText is a rendering boundary, never a mutation of Agent input or
// output. Provider/tool escape sequences cannot control the user's terminal.
func displayText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, ansi.Strip(text))
}

func wrap(text string, width int) []string {
	width = max(width, 1)
	lines := []string{""}
	column := 0
	g := uniseg.NewGraphemes(displayText(text))
	for g.Next() {
		cluster := g.Str()
		if cluster == "\n" {
			lines = append(lines, "")
			column = 0
			continue
		}
		if cluster == "\t" {
			cluster = "    "
		}
		cells := uniseg.StringWidth(cluster)
		if column+cells > width && column != 0 {
			lines = append(lines, "")
			column = 0
		}
		if cells > width {
			cluster, cells = "…", 1
		}
		lines[len(lines)-1] += cluster
		column += cells
	}
	return lines
}

func (s *State) transcriptLines() []string {
	var lines []string
	lastThinking := -1
	for index, entry := range s.Entries {
		if entry.Kind == "thinking" && !entry.Redacted {
			lastThinking = index
		}
	}
	for index, entry := range s.Entries {
		if entry.TranscriptOnly && !s.Transcript {
			continue
		}
		if s.Transcript && entry.Kind == "thinking" && !entry.Redacted && index != lastThinking {
			continue
		}
		var label string
		text := entry.Text
		switch entry.Kind {
		case "user":
			label = "You: "
		case "thinking":
			if entry.Redacted {
				lines = append(lines, wrap("✻ Thinking…", s.Width)...)
				continue
			}
			label = "Thinking: "
		case "tool":
			label = "✓ " + entry.ToolName + ": "
			if entry.IsError {
				label = "✗ " + entry.ToolName + ": "
			}
			if entry.Running {
				label = "… " + entry.ToolName + ": "
			}
		case "assistant":
			label = "Claude: "
		case "teammate":
			row := presentationObject(entry.Input)
			label = "@" + row.String("from") + "› "
			if s.Transcript {
				text = row.String("summary") + "\n" + text
			} else {
				text = row.String("summary")
			}
		case "local_output":
			label = "⎿ "
		case "system":
			label = "● "
		case "bash_output":
			label = ""
		default:
			label = entry.Kind + ": "
		}
		if s.Transcript && entry.Kind == "tool" && len(entry.Output) > 0 {
			text = toolInputPresentation(entry.ToolName, entry.Input) + "\n" + toolResultPresentation(entry, true)
		}
		lines = append(lines, wrap(label+text, s.Width)...)
		lines = append(lines, "")
	}
	return lines
}

func (s *State) editorLines() ([]string, int, int) {
	prefix := "> "
	if s.Mode == "bash" {
		prefix = "! "
	}
	if s.Width < 3 {
		prefix = ""
	}
	prefixWidth := uniseg.StringWidth(prefix)
	lines := []string{prefix}
	x, y, offset := prefixWidth, 0, 0
	cursorX, cursorY := x, y
	g := uniseg.NewGraphemes(s.Editor.Text())
	for g.Next() {
		cluster := g.Str()
		start, end := g.Positions()
		if cluster == "\n" {
			if start == s.Editor.Cursor() {
				cursorX, cursorY = x, y
			}
			lines = append(lines, strings.Repeat(" ", prefixWidth))
			y++
			x = prefixWidth
			offset = end
			if offset == s.Editor.Cursor() {
				cursorX, cursorY = x, y
			}
			continue
		}
		visible := displayText(cluster)
		cells := uniseg.StringWidth(visible)
		if x+cells > s.Width {
			lines = append(lines, strings.Repeat(" ", prefixWidth))
			y++
			x = prefixWidth
		}
		if cells > s.Width-prefixWidth {
			visible, cells = "…", 1
		}
		if start == s.Editor.Cursor() {
			cursorX, cursorY = x, y
		}
		lines[len(lines)-1] += visible
		x += cells
		offset = end
		if offset == s.Editor.Cursor() {
			cursorX, cursorY = x, y
		}
	}
	if cursorX >= s.Width {
		lines = append(lines, strings.Repeat(" ", prefixWidth))
		cursorX, cursorY = prefixWidth, len(lines)-1
	}
	return lines, cursorX, cursorY
}

func (s *State) View(title string, fullscreen bool) tea.View {
	header := wrap(title, s.Width)
	if title == "" {
		header = nil
	}
	footer := []string{}
	var cursor *tea.Cursor
	if s.Doctor != nil {
		footer = s.Doctor.lines(s.Width, s.Height)
	} else if s.DreamDialog != nil {
		footer = s.dreamLines()
	} else if s.MessageSelector != nil {
		footer = s.MessageSelector.lines(s.Width)
	} else if s.WorktreeExit != nil {
		footer = s.WorktreeExit.lines(s.Width)
	} else if s.ThinkingPicker != nil {
		footer = s.ThinkingPicker.lines(s.Width)
	} else if s.ModelPicker != nil {
		footer = s.ModelPicker.lines(s.Width)
	} else if s.IDEPicker != nil {
		footer = s.IDEPicker.lines(s.Width)
	} else if s.ideOnboardingVisible() {
		footer = s.IDEOnboarding.lines(s.Width)
	} else if s.pluginHintVisible() {
		footer = s.PluginHint.lines(s.Width)
	} else if s.Transcript {
		footer = wrap("Transcript · Esc to return", s.Width)
	} else if len(s.Permissions) > 0 {
		permission := s.Permissions[0]
		if permission.ToolName == "AskUserQuestion" {
			footer = s.questionLines(permission, s.Width)
		} else {
			title := permission.Title
			if title == "" {
				title = "Allow " + permission.ToolName + "?"
			}
			footer = append(footer, wrap(title, s.Width)...)
			footer = append(footer, wrap(permission.Description, s.Width)...)
			if permission.Title == "" {
				footer = append(footer, wrap(toolInputPresentation(permission.ToolName, permission.Input), s.Width)...)
			}
			if permission.DisplayOnly {
			} else if len(permission.Choices) == 0 {
				footer = append(footer, wrap("y/Enter: allow · n/Esc: deny", s.Width)...)
			} else {
				for i, choice := range permission.Choices {
					prefix := "  "
					if i == s.PermissionSelected {
						prefix = "> "
					}
					label := prefix + choice.Label
					if choice.Editable {
						label += ": "
						value := s.permissionChoiceValue(permission, i)
						if i == s.PermissionSelected {
							position := len(value)
							if editor := s.permissionEditor; editor != nil && editor.id == permission.ID && editor.index == i {
								position = editor.editor.Cursor()
							}
							before := wrap(label+value[:position], s.Width)
							cursor = tea.NewCursor(min(uniseg.StringWidth(before[len(before)-1]), s.Width-1), len(footer)+len(before)-1)
						}
						label += value
					}
					footer = append(footer, wrap(label, s.Width)...)
				}
				if s.permissionEditableSelected() {
					footer = append(footer, wrap("Type: edit rule · ↑/↓: choose · Enter: confirm · Esc: deny", s.Width)...)
				} else {
					footer = append(footer, wrap("↑/↓: choose · Enter: confirm · Esc: deny", s.Width)...)
				}
			}
		}
	} else {
		input, x, y := s.editorLines()
		maxInput := min(8, max(1, s.Height-2))
		start := max(0, y-maxInput+1)
		end := min(len(input), start+maxInput)
		footer = append(footer, input[start:end]...)
		cursor = tea.NewCursor(min(x, s.Width-1), y-start)
		if s.Searching {
			footer = append(footer, wrap("History search: "+s.Search, s.Width)...)
		} else if len(s.Completions) > 0 {
			footer = append(footer, s.completionLines(fullscreen)...)
			footer = append(footer, wrap("↑/↓: choose · Tab: complete · Esc: dismiss", s.Width)...)
		} else if s.ArgumentHint != "" {
			footer = append(footer, wrap(s.ArgumentHint, s.Width)...)
		}
	}
	if s.Notification != "" {
		footer = append(footer, wrap(s.Notification, s.Width)...)
	}
	if s.PermissionMode != "" && s.PermissionMode != "default" {
		label := map[string]string{"acceptEdits": "accept edits on", "plan": "plan mode on", "bypassPermissions": "bypass permissions on", "auto": "auto mode on"}[s.PermissionMode]
		if label != "" {
			if shortcut := s.modeCycleShortcut(); shortcut != "" {
				label += " · " + shortcut + " to cycle"
			}
			footer = append(footer, wrap(label, s.Width)...)
		}
	}
	if s.Status != "" {
		footer = append(footer, wrap(s.Status, s.Width)...)
	}
	footer = append(footer, s.teammateTreeLines()...)
	if s.DreamDialog == nil {
		footer = append(footer, s.dreamFooterLines()...)
	}
	footer = append(footer, s.teammateInboxLines()...)
	if len(s.Queue) > 0 {
		footer = append(footer, wrap(fmt.Sprintf("%d queued", len(s.Queue)), s.Width)...)
	}
	// Narrow/short terminals retain the input or current approval instead of
	// rendering outside the viewport. Resizing cannot change submission bytes.
	if len(footer) >= s.Height {
		footer = footer[:s.Height]
		header = nil
	}
	if len(header)+len(footer) >= s.Height {
		header = nil
	}
	available := max(0, s.Height-len(header)-len(footer))
	body := s.transcriptLines()
	if teammate, viewing := s.teammateTranscriptLines(); viewing {
		body = teammate
	}
	end := max(min(len(body)-s.Scroll, len(body)), min(available, len(body)))
	start := max(0, end-available)
	if available == 0 {
		body = nil
	} else {
		body = body[start:end]
	}
	lines := append(append(header, body...), footer...)
	if cursor != nil {
		cursor.Y += len(header) + len(body)
		if cursor.Y >= len(lines) {
			cursor = nil
		}
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.Cursor, view.AltScreen = cursor, fullscreen
	view.ReportFocus = true
	return view
}
