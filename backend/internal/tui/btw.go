package tui

import (
	"math"
	"runtime"
	"strings"
	"time"
)

func (s *State) handleTerminalBtwKey(key Key) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "btw" {
		return false, nil
	}
	switch key.Name {
	case "esc", "escape", "enter", "space", "ctrl+c", "ctrl+d":
		return true, []action{{kind: actionLocalPanel, panel: p.result("btw-close")}}
	case "up", "ctrl+p":
		p.scroll = max(0, p.scroll-3)
	case "down", "ctrl+n":
		p.scroll += 3
	default:
		if key.Text == " " {
			return true, []action{{kind: actionLocalPanel, panel: p.result("btw-close")}}
		}
	}
	return true, nil
}

func (p *localPanelState) terminalBtwLines(width, height int, theme string, now time.Time) []string {
	value := presentationObject(p.props.Presentation)
	question := value.String("question")
	response := value.String("response")
	failure := value.String("error")
	lines := []string{""}
	available := max(1, width-2)
	basis := 0
	for _, line := range exportWrapWords(question, available) {
		basis = max(basis, exportWidth(line))
	}
	prefixWidth := float64(5)
	if basis+5 > available {
		prefixWidth = float64(5*available) / float64(basis+5)
	}
	questionLeft := int(math.Floor(prefixWidth))
	questionRows := exportWrapWords(question, max(1, available-questionLeft))
	for i, line := range questionRows {
		prefix := strings.Repeat(" ", questionLeft)
		if i == 0 {
			prefix = buddyStyle(exportTruncate("/btw ", questionLeft, false), theme, "warning", true, false, false, false)
		}
		lines = append(lines, "  "+prefix+buddyStyle(line, theme, "", false, false, true, false))
	}
	lines = append(lines, "")
	body := []string{}
	if failure != "" {
		for _, line := range exportWrapWords(failure, max(1, width-4)) {
			color := map[string]string{"dark": "rgb(255,107,128)", "light": "rgb(171,43,63)", "dark-daltonized": "rgb(255,102,102)", "light-daltonized": "rgb(204,0,0)", "dark-ansi": "ansi:redBright", "light-ansi": "ansi:red"}[theme]
			body = append(body, terminalThemeColorEscape(color, false)+displayText(line)+"\x1b[0m")
		}
	} else if response != "" {
		body = TerminalMarkdownRows(response, max(1, width-4), width, theme)
	} else {
		frames := []string{"·", "✢", "✳", "✶", "✻", "✽"}
		if string(value.Get("ghostty")) == "true" {
			frames[5] = "*"
		}
		if runtime.GOOS != "darwin" {
			frames[2] = "*"
		}
		frames = append(frames, frames[5], "✻", "✶", frames[2], "✢", "·")
		frame := 0
		if !p.btwStarted.IsZero() && !now.Before(p.btwStarted) {
			frame = int(now.Sub(p.btwStarted) / (80 * time.Millisecond))
		}
		body = []string{buddyStyle(frames[frame%len(frames)]+" Answering...", theme, "warning", false, false, false, false)}
	}
	visible := max(5, height-11)
	if response != "" {
		// The outer margin box measures at its parent width before the two
		// left-margin cells constrain ScrollBox's inner content. The original
		// renderer follows the previous bottom when that final content grows.
		measured := len(terminalMarkdownRows(response, max(1, width-2), width, theme))
		visible = max(1, min(visible, measured))
		if !p.btwBodyInitialized {
			p.scroll = max(0, len(body)-visible)
			p.btwBodyInitialized = true
		}
	}
	p.scroll = min(p.scroll, max(0, len(body)-visible))
	for _, line := range body[p.scroll:min(len(body), p.scroll+visible)] {
		lines = append(lines, "    "+line)
	}
	if response != "" || failure != "" {
		lines = append(lines, "")
		for _, line := range buddyWrapStyled(buddyStyle("↑/↓ to scroll · Space, Enter, or Escape to dismiss", theme, "", false, false, true, false), max(1, width-2)) {
			lines = append(lines, "  "+line)
		}
	}
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}

func (s *State) applyTerminalImmediateNotice(text string, now time.Time) {
	if s.pluginNotices == nil {
		s.pluginNotices = &terminalPluginNotices{}
	}
	state := s.pluginNotices
	if s.Notification != "" && state.current != "immediate-btw" {
		key := state.current
		if key == "" {
			key = "ordinary-current"
		}
		priority := 1
		if key == "plugin-delisted-flagged" {
			priority = 2
		} else if key == "plugin-reload-pending" {
			priority = 0
		}
		state.queue = append([]terminalPluginNotice{{key, s.Notification, priority}}, state.queue...)
	}
	state.current = "immediate-btw"
	s.Notification = text
	s.notificationUntil = now.Add(8 * time.Second)
}
