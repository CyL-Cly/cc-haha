package tui

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"strings"
	"time"
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
		case "context_usage":
			var data contextusage.Data
			if json.Unmarshal(entry.Input, &data) == nil {
				lines = append(lines, ContextVisualization(data, s.Width)...)
			}
			continue
		case "user":
			if projected, handled := terminalLocalUserMessageLines(text, s.Width, s.previewTheme()); handled {
				if len(projected) > 0 {
					lines = append(lines, projected...)
					lines = append(lines, "")
				}
				continue
			}
			if projected, handled := s.terminalTeammateHistoricalUserLines(text, index); handled {
				lines = append(lines, projected...)
				continue
			}
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
			if entry.Subtype == string(TeammateMessageAttachment) && !s.taskTeamsEnabled {
				continue
			}
			lines = append(lines, terminalTeammateMessageLines(presentationObject(entry.Input), s.Width, s.previewTheme(), s.Transcript, TeammateMessageOrigin(entry.Subtype))...)
			continue
		case "local_output":
			label = "⎿ "
		case "system":
			if entry.Subtype == "local_command" {
				projected, handled := terminalLocalUserMessageLines(text, s.Width, s.previewTheme())
				if !handled {
					projected = exportWrapWords("❯ "+displayText(text), max(1, s.Width-1))
				}
				if len(projected) > 0 {
					lines = append(lines, projected...)
					lines = append(lines, "")
				}
				continue
			}
			label = "● "
		case "turn_duration":
			if !s.Preferences.ShowTurnDuration {
				continue
			}
			label = "✻ "
		case "bash_output":
			label = ""
		default:
			label = entry.Kind + ": "
		}
		if (s.Transcript || s.Preferences.Verbose) && entry.Kind == "tool" && len(entry.Output) > 0 {
			text = toolInputPresentation(entry.ToolName, entry.Input) + "\n" + toolResultPresentation(entry, true)
		}
		lines = append(lines, wrap(label+text, s.Width)...)
		lines = append(lines, "")
	}
	return lines
}

func (s *State) editorLines() ([]string, int, int) {
	width := max(1, s.Width-s.buddyReservedColumns())
	prefix := "> "
	if s.SessionName != "" {
		prefix = displayText(s.SessionName) + "> "
	}
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
		if x+cells > width {
			lines = append(lines, strings.Repeat(" ", prefixWidth))
			y++
			x = prefixWidth
		}
		if cells > width-prefixWidth {
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
	if cursorX >= width {
		lines = append(lines, strings.Repeat(" ", prefixWidth))
		cursorX, cursorY = prefixWidth, len(lines)-1
	}
	return lines, cursorX, cursorY
}

func (s *State) View(title string, fullscreen bool) tea.View {
	if s.Transcript && s.LocalPanel != nil && s.LocalPanel.props.HelpDialog {
		return s.terminalHelpTranscriptView(fullscreen)
	}
	header := wrap(title, s.Width)
	if title == "" {
		header = nil
	}
	footer := []string{}
	inputVisible := false
	var cursor *tea.Cursor
	if s.ModelPicker != nil && s.LocalPanel != nil {
		footer = s.ModelPicker.lines(s.Width)
	} else if s.LocalPanel != nil && s.LocalPanel.props.ThemePicker {
		shortcut, ok := BindingDisplay("theme:toggleSyntaxHighlighting", "ThemePicker", s.bindings)
		if !ok {
			shortcut = "ctrl+t"
		}
		footer = s.LocalPanel.terminalThemePickerLines(s.Width, s.Height, shortcut)
	} else if s.LocalPanel != nil && s.LocalPanel.props.Kind == "diff" {
		footer = s.LocalPanel.terminalDiffLines(s.Width, s.Height, s.Preferences.Theme, s.bindings)
	} else if s.LocalPanel != nil && s.LocalPanel.props.Kind == "btw" {
		footer = s.LocalPanel.terminalBtwLines(s.Width, s.Height, s.Preferences.Theme, time.Now())
		if s.LocalPanel.props.KeepPromptInput {
			inputVisible = true
			input, _, _ := s.editorLines()
			footer = append(footer, input...)
		}
	} else if s.LocalPanel != nil && s.LocalPanel.props.Kind == "buddy" {
		footer = terminalBuddyCardLines(s.LocalPanel.props.Presentation, s.Width, s.Preferences.Theme)
	} else if s.LocalPanel != nil && s.LocalPanel.props.Kind == "resume" {
		footer = s.LocalPanel.terminalResumeLines(s.Width, s.Height, time.Now(), s.bindings)
	} else if s.LocalPanel != nil && s.LocalPanel.props.Kind == "teams" {
		footer = s.LocalPanel.terminalTeamsLines(s.Width, s.Height, s.previewTheme(), s.bindings)
	} else if s.LocalPanel != nil && s.LocalPanel.props.HelpDialog {
		if fullscreen {
			footer = s.LocalPanel.terminalFullscreenHelpLines(s.Width, s.Height, s.previewTheme(), s.bindings)
		} else {
			footer = s.LocalPanel.terminalHelpLines(s.Width, s.Height, s.previewTheme(), s.bindings)
		}
	} else if s.LocalPanel != nil {
		footer = s.LocalPanel.lines(s.Width, s.Height, s.Preferences.Theme)
	} else if s.Doctor != nil {
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
			if permission.IDEName != "" {
				footer = append(footer, wrap("Opened changes in "+permission.IDEName+" ⧉", s.Width)...)
			}
			footer = append(footer, wrap(permission.Description, s.Width)...)
			if permission.Title == "" {
				footer = append(footer, wrap(toolInputPresentation(permission.ToolName, permission.Input), s.Width)...)
			}
			if permission.DisplayOnly {
			} else if s.PermissionDebugVisible(permission) {
				footer = append(footer, PermissionDebugLines(permission, s.Width)...)
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
		if permission.Debug != nil && permission.Debug.DebugHint && !s.PermissionDebugVisible(permission) {
			if binding, ok := BindingDisplay("permission:toggleDebug", "Confirmation", s.bindings); ok {
				footer = append(footer, wrap(binding+" to show debug info", s.Width)...)
			}
		}
	} else {
		inputVisible = true
		input, x, y := s.editorLines()
		maxInput := min(8, max(1, s.Height-2))
		start := max(0, y-maxInput+1)
		end := min(len(input), start+maxInput)
		visible, offset := s.joinBuddyEditor(input[start:end], fullscreen)
		footer = append(footer, visible...)
		cursor = tea.NewCursor(min(x, s.Width-1), y-start+offset)
		if s.selectedTerminalFooter() != "" {
			cursor = nil
		}
		if s.Searching {
			footer = append(footer, wrap("History search: "+s.Search, s.Width)...)
		} else if len(s.Completions) > 0 {
			footer = append(footer, s.completionLines(fullscreen)...)
			footer = append(footer, wrap("↑/↓: choose · Tab: complete · Esc: dismiss", s.Width)...)
		} else if s.HelpOpen {
			footer = append(footer, s.shortcutHelpRows()...)
		} else if s.ArgumentHint != "" {
			footer = append(footer, wrap(s.ArgumentHint, s.Width)...)
		}
		if mode := s.VimMode(); mode != "" {
			footer = append(footer, wrap(mode, s.Width)...)
		}
	}
	if fullscreen && s.LocalPanel != nil && s.LocalPanel.props.HelpDialog {
		// FullscreenLayout's opaque local-JSX pane replaces PromptInput. Its
		// bottom widgets are not children of Help or part of the modal height.
		return s.terminalFullscreenHelpView(footer)
	}
	if inputVisible && s.terminalStatusLineInputMounted(fullscreen) {
		footer = append(footer, s.terminalStatusLineLines(fullscreen)...)
	}
	if s.Notification != "" {
		footer = append(footer, s.terminalPluginNotificationLines()...)
	}
	if s.keybindingWarning != "" {
		footer = append(footer, wrap(s.keybindingWarning, s.Width)...)
	}
	if !s.HelpOpen && s.PermissionMode != "" && s.PermissionMode != "default" {
		label := map[string]string{"acceptEdits": "accept edits on", "plan": "plan mode on", "bypassPermissions": "bypass permissions on", "auto": "auto mode on"}[s.PermissionMode]
		if label != "" {
			if shortcut := s.modeCycleShortcut(); shortcut != "" {
				label += " · " + shortcut + " to cycle"
			}
			footer = append(footer, wrap(label, s.Width)...)
		}
	}
	spinner := s.terminalSpinnerLines(s.dreamClock)
	spinnerStatus := s.Status == "Working…" || s.spinnerClock != nil && s.Status == s.spinnerClock.verb+"…"
	if s.Status != "" && !spinnerStatus {
		status := s.Status
		if status == "Working…" && s.Preferences.Theme != "" && !s.Preferences.ReducedMotion {
			frames := []string{"·", "✢", "✳", "✶", "✻", "✽"}
			status = frames[(s.dreamClock.UnixMilli()/200)%int64(len(frames))] + " " + status
		}
		footer = append(footer, wrap(status, s.Width)...)
		if len(spinner) == 0 && s.Busy() && s.Preferences.ShowTips && s.Preferences.SpinnerTip != "" {
			footer = append(footer, wrap("Tip: "+s.Preferences.SpinnerTip, s.Width)...)
		}
	}
	if len(spinner) == 0 {
		footer = append(footer, s.teammateTreeLines()...)
	}
	footer = append(footer, s.terminalUpdateLines()...)
	for _, line := range s.terminalRemoteLines() {
		footer = append(footer, wrap(line, s.Width)...)
	}
	if !s.HelpOpen {
		footer = append(footer, s.terminalPRFooterLines()...)
	}
	if mount := s.terminalTaskListMount(); mount == "standalone" || mount == "spinner" && len(spinner) == 0 {
		footer = append(s.expandedTaskLines(), footer...)
	}
	if len(spinner) > 0 {
		footer = append(spinner, footer...)
		if cursor != nil {
			cursor.Y += len(spinner)
		}
	}
	footer = append(footer, s.terminalNavigationFooterLines()...)
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
	if accent := s.promptAccent(); accent != "" {
		for i, line := range lines {
			if strings.HasPrefix(line, "> ") || s.SessionName != "" && strings.HasPrefix(line, displayText(s.SessionName)+"> ") || i < len(header) {
				lines[i] = accent + line + "\x1b[39m"
			}
		}
	}
	if cursor != nil {
		cursor.Y += len(header) + len(body)
		if cursor.Y >= len(lines) {
			cursor = nil
		}
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.Cursor, view.AltScreen = cursor, fullscreen
	if s.Preferences.ProgressBarEnabled && s.Busy() {
		view.ProgressBar = &tea.ProgressBar{State: tea.ProgressBarIndeterminate}
	}
	view.ReportFocus = true
	return view
}
