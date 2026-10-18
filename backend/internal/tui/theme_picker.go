package tui

import (
	"strings"
	"time"
)

type TerminalThemeOption struct{ ID, Label string }

func TerminalThemeOptions(autoBuildFeature bool) []TerminalThemeOption {
	options := []TerminalThemeOption{}
	if autoBuildFeature {
		options = append(options, TerminalThemeOption{"auto", "Auto (match terminal)"})
	}
	return append(options, TerminalThemeOption{"dark", "Dark mode"}, TerminalThemeOption{"light", "Light mode"}, TerminalThemeOption{"dark-daltonized", "Dark mode (colorblind-friendly)"}, TerminalThemeOption{"light-daltonized", "Light mode (colorblind-friendly)"}, TerminalThemeOption{"dark-ansi", "Dark mode (ANSI colors only)"}, TerminalThemeOption{"light-ansi", "Light mode (ANSI colors only)"})
}

func (s *State) previewTheme() string {
	p := s.LocalPanel
	if p != nil && p.props.ThemePicker && len(p.props.Rows) > 0 {
		if p.themePreview != "" {
			return p.themePreview
		}
		return p.props.Rows[min(max(p.selected, 0), len(p.props.Rows)-1)].ID
	}
	return s.Preferences.Theme
}

func (s *State) handleTerminalThemeKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || !p.props.ThemePicker {
		return false, nil
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	if name == "ctrl+c" || name == "ctrl+d" {
		return false, nil
	}
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Confirmation", "Select", "ThemePicker"}, s.bindings, s.pendingChord)
	switch resolution.Type {
	case "chord_started":
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	case "chord_cancelled", "unbound":
		s.pendingChord = nil
		return true, nil
	case "match":
		s.pendingChord = nil
		switch resolution.Action {
		case "theme:toggleSyntaxHighlighting":
			name = "toggle-syntax"
		case "confirm:no":
			name = "esc"
		case "select:accept", "confirm:yes":
			name = "enter"
		case "select:next", "confirm:next":
			name = "down"
		case "select:previous", "confirm:previous":
			name = "up"
		default:
			return false, nil
		}
	}
	count := len(p.props.Rows)
	switch name {
	case "down", "j", "ctrl+n":
		if count > 0 {
			p.selected = (p.selected + 1) % count
			p.themePreview = p.props.Rows[p.selected].ID
		}
	case "up", "k", "ctrl+p":
		if count > 0 {
			p.selected = (p.selected + count - 1) % count
			p.themePreview = p.props.Rows[p.selected].ID
		}
	case "enter":
		return true, []action{{kind: actionLocalPanel, panel: p.result("theme-select")}}
	case "esc":
		return true, []action{{kind: actionLocalPanel, panel: p.result("theme-cancel")}}
	case "toggle-syntax":
		meta := presentationObject(p.props.Presentation)
		if meta.String("colorModuleUnavailableReason") == "env" {
			return true, nil
		}
		return true, []action{{kind: actionLocalPanel, panel: p.result("theme-toggle-syntax")}}
	default:
		if strings.HasPrefix(name, "ctrl+") {
			return false, nil
		}
	}
	return true, nil
}
