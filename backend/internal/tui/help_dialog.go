package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

func (p *localPanelState) terminalHelpData() TerminalHelpData {
	var data TerminalHelpData
	_ = json.Unmarshal(p.props.Presentation, &data)
	return data
}
func (p *localPanelState) terminalHelpTab() string {
	if p.helpTab == "" {
		return "general"
	}
	return p.helpTab
}
func (p *localPanelState) terminalHelpCommands() []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	for _, row := range p.terminalHelpData().Commands {
		builtin := string(row.Get("builtin")) == "true"
		if p.terminalHelpTab() == "commands" && builtin || p.terminalHelpTab() == "custom-commands" && !builtin {
			rows = append(rows, row)
		}
	}
	return rows
}
func (s *State) handleTerminalHelpKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || !p.props.HelpDialog {
		return false, nil
	}
	if handled, actions := s.handleTerminalRootHelpKey(key, now); handled {
		return true, actions
	}
	if s.fullscreen {
		resolution := ResolveChord(keyStroke(key), []string{"Scroll"}, s.bindings, s.pendingChord)
		if resolution.Type == "match" {
			viewport := max(0, s.Height-1) // FullscreenLayout's transcript paddingTop=1.
			limit := max(0, len(s.transcriptLines())-viewport)
			switch resolution.Action {
			case "scroll:pageUp":
				s.Scroll = min(limit, s.Scroll+max(1, viewport/2))
				return true, nil
			case "scroll:pageDown":
				s.Scroll = max(0, s.Scroll-max(1, viewport/2))
				return true, nil
			case "scroll:lineUp":
				s.Scroll = min(limit, s.Scroll+1)
				return true, nil
			case "scroll:lineDown":
				s.Scroll = max(0, s.Scroll-1)
				return true, nil
			case "scroll:top":
				s.Scroll = limit
				return true, nil
			case "scroll:bottom":
				s.Scroll = 0
				return true, nil
			}
		}
	}
	contexts := []string{"Global", "Help"}
	if p.headerFocused {
		contexts = append(contexts, "Tabs")
	} else {
		contexts = append(contexts, "Select")
	}
	resolution := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	close := func() []action {
		s.LocalPanel = nil
		return []action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: p.props.ID, Action: "help-close"}}}
	}
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	}
	if resolution.Type == "unbound" {
		s.pendingChord = nil
		return true, nil
	}
	if resolution.Type == "match" {
		s.pendingChord = nil
		switch resolution.Action {
		case "select:next":
			key.Name = "down"
		case "select:previous":
			key.Name = "up"
		case "help:dismiss", "select:cancel":
			return true, close()
		case "app:interrupt", "app:exit":
			if p.helpExitTimes == nil {
				p.helpExitTimes = map[string]time.Time{}
			}
			if previous, ok := p.helpExitTimes[resolution.Action]; ok && !now.Before(previous) && now.Sub(previous) <= doublePressWindow {
				return true, close()
			}
			p.helpExitTimes[resolution.Action] = now
			p.helpExitPending = "Ctrl-C"
			if resolution.Action == "app:exit" {
				p.helpExitPending = "Ctrl-D"
			}
			return true, nil
		case "tabs:next", "tabs:previous":
			tabs := []string{"general", "commands", "custom-commands"}
			index := 0
			for i, tab := range tabs {
				if tab == p.terminalHelpTab() {
					index = i
				}
			}
			step := 1
			if resolution.Action == "tabs:previous" {
				step = -1
			}
			p.helpTab = tabs[(index+len(tabs)+step)%len(tabs)]
			p.headerFocused = true
			p.selected = 0
			p.scroll = 0
			if p.helpTab == "general" {
				return true, []action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: p.props.ID, Action: "help-remount-general"}}}
			}
			return true, nil
		}
	}
	if p.terminalHelpTab() != "general" {
		switch key.Name {
		case "down":
			if p.headerFocused {
				p.headerFocused = false
			} else {
				count := len(p.terminalHelpCommands())
				if count > 0 {
					p.selected = (p.selected + 1) % count
					visible := max(1, (s.Height/2-10)/2)
					if p.selected == 0 {
						p.scroll = 0
					} else if p.selected >= p.scroll+visible {
						p.scroll++
					}
				}
			}
		case "up":
			if p.selected == 0 {
				p.headerFocused = true
			} else {
				p.selected--
				if p.selected <= p.scroll && p.scroll > 0 {
					p.scroll--
				}
			}
		}
	}
	return true, nil
}

func (s *State) tickTerminalHelpExit(now time.Time) {
	p := s.LocalPanel
	if p == nil || !p.props.HelpDialog {
		return
	}
	for key, at := range p.helpExitTimes {
		if now.Sub(at) >= doublePressWindow {
			delete(p.helpExitTimes, key)
			p.helpExitPending = ""
		}
	}
}

func terminalHelpBlue(theme string) string {
	if theme == "light-ansi" {
		return "\x1b[94m"
	}
	return "\x1b[38;2;106;155;204m"
}
func terminalHelpDescription(text string, width int) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return ansi.Truncate(displayText(text[:index])+"…", max(1, width), "…")
	}
	return ansi.Truncate(displayText(text), max(1, width), "…")
}
func (p *localPanelState) terminalHelpLines(width, height int, theme string, bindings []Keybinding) []string {
	return p.terminalHelpFlexLines(width, height, theme, bindings)
}
