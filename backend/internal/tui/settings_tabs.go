package tui

import "time"

func (s *State) handleTerminalSettingsTabs(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || len(p.props.SettingsTabs) == 0 || p.props.Submenu || !p.headerFocused {
		return false, nil
	}
	resolution := ResolveChord(keyStroke(key), []string{"Tabs", "Settings", "Global"}, s.bindings, s.pendingChord)
	if resolution.Type == "chord_started" {
		s.pendingChord, s.chordUntil = resolution.Pending, now.Add(time.Second)
		return true, nil
	}
	if resolution.Type == "match" && (resolution.Action == "tabs:next" || resolution.Action == "tabs:previous") {
		s.pendingChord = nil
		index := 0
		for i, kind := range p.props.SettingsTabs {
			if kind == p.props.Kind {
				index = i
				break
			}
		}
		direction := 1
		if resolution.Action == "tabs:previous" {
			direction = -1
		}
		kind := p.props.SettingsTabs[(index+len(p.props.SettingsTabs)+direction)%len(p.props.SettingsTabs)]
		return true, []action{{kind: actionLocalPanel, panel: p.result("settings-tab:" + kind)}}
	}
	if resolution.Type == "match" && resolution.Action == "settings:retry" && p.props.Kind == "usage" {
		s.pendingChord = nil
		for _, registered := range p.props.Actions {
			if registered.Name == "usage-retry" {
				return true, []action{{kind: actionLocalPanel, panel: p.result("usage-retry")}}
			}
		}
		return true, nil
	}
	if key.Name == "down" && p.props.Kind == "config" {
		p.headerFocused = false
		return true, nil
	}
	if key.Name == "esc" || key.Name == "escape" {
		// Header Escape belongs to Settings; Config's revert callback is only
		// active while its content has focus.
		s.LocalPanel = nil
		return true, []action{{kind: actionLocalPanel, panel: p.result("settings-dismiss")}}
	}
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		return false, nil
	}
	return true, nil
}
