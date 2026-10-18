package tui

import (
	"strings"
	"time"
)

func (p *localPanelState) filterSettingsRows() {
	if p.props.Kind != "config" || p.props.Submenu {
		return
	}
	query := strings.ToLower(p.settingsSearch.Text())
	p.props.Rows = nil
	for _, row := range p.settingsRows {
		label := row.SearchText
		if label == "" {
			label = row.Label
		}
		if query == "" || strings.Contains(strings.ToLower(row.ID), query) || strings.Contains(strings.ToLower(label), query) {
			p.props.Rows = append(p.props.Rows, row)
		}
	}
	p.selected = min(p.selected, max(0, len(p.props.Rows)-1))
}

func (s *State) handleLocalSettingsKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "config" || len(p.editors) > 0 || p.props.Submenu {
		return false, nil
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	if p.searchFocused {
		switch name {
		case "up":
			if len(p.props.SettingsTabs) > 0 {
				p.headerFocused = true
			}
		case "ctrl+c", "ctrl+d":
			return false, nil
		case "esc", "escape":
			if p.settingsSearch.Text() != "" {
				p.settingsSearch.Set("")
				p.selected = 0
				p.filterSettingsRows()
			} else {
				p.searchFocused = false
			}
		case "enter", "down", "wheeldown":
			p.searchFocused = false
			p.selected = 0
			p.scroll = 0
		case "backspace":
			p.settingsSearch.Backspace()
			p.selected = 0
			p.filterSettingsRows()
		case "delete":
			p.settingsSearch.Delete()
			p.selected = 0
			p.filterSettingsRows()
		case "left":
			p.settingsSearch.Left()
		case "right":
			p.settingsSearch.Right()
		case "home", "ctrl+a":
			p.settingsSearch.Home()
		case "end", "ctrl+e":
			p.settingsSearch.End()
		case "ctrl+u":
			p.settingsSearch.KillToStart()
			p.selected = 0
			p.filterSettingsRows()
		default:
			if key.Text != "" {
				p.settingsSearch.Insert(key.Text)
				p.selected = 0
				p.filterSettingsRows()
			}
		}
		return true, nil
	}
	resolution := ResolveChord(keyStroke(key), []string{"Settings", "Global"}, s.bindings, s.pendingChord)
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	}
	if resolution.Type == "match" {
		s.pendingChord = nil
		switch resolution.Action {
		case "settings:search":
			p.searchFocused = true
			p.settingsSearch.Set("")
			p.filterSettingsRows()
			return true, nil
		case "settings:close":
			return true, []action{{kind: actionLocalPanel, panel: p.result("commit-config")}}
		case "select:previous":
			if p.selected == 0 {
				p.searchFocused = true
				p.scroll = 0
			} else {
				p.selected--
			}
			return true, nil
		case "select:next":
			p.selected = min(p.selected+1, max(0, len(p.props.Rows)-1))
			return true, nil
		case "select:accept":
			name = "right"
		case "confirm:no":
			return false, nil
		}
	}
	if name == "left" || name == "right" || name == "tab" || name == "space" {
		if len(p.props.Rows) == 0 {
			return true, nil
		}
		for _, choice := range p.props.Rows[p.selected].Actions {
			// The Source left/right/tab paths all cycle forward.
			if choice.Key == "right" {
				return true, []action{{kind: actionLocalPanel, panel: p.result(choice.Name)}}
			}
		}
		return true, nil
	}
	if key.Text != "" && key.Text != "j" && key.Text != "k" && key.Text != "/" && key.Text != " " && !strings.HasPrefix(name, "ctrl+") && !strings.HasPrefix(name, "alt+") {
		p.settingsSearch.Set(key.Text)
		p.searchFocused = true
		p.selected = 0
		p.filterSettingsRows()
		return true, nil
	}
	return false, nil
}
