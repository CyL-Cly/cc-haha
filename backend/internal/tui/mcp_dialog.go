package tui

import "time"

// Each mounted Source MCP component owns two independent double-press refs.
// Refreshing its immutable props retains those refs; navigating to another
// component or closing the panel retires them.
func (s *State) handleMCPDialogExit(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.MCPExitScope == "" || (key.Name != "ctrl+c" && key.Name != "ctrl+d") {
		return false, nil
	}
	if p.mcpExitTimes == nil {
		p.mcpExitTimes = map[string]time.Time{}
	}
	previous, exists := p.mcpExitTimes[key.Name]
	if exists && !now.Before(previous) && now.Sub(previous) <= doublePressWindow {
		delete(p.mcpExitTimes, key.Name)
		p.mcpExitPending = ""
		return true, s.EOF()
	}
	p.mcpExitTimes[key.Name] = now
	// MCPListPanel intentionally mounts Dialog with hideInputGuide=true.
	if p.props.MCPExitScope != "list" {
		name := "Ctrl-D"
		if key.Name == "ctrl+c" {
			name = "Ctrl-C"
		}
		p.mcpExitPending = "Press " + name + " again to exit"
	}
	return true, nil
}

func (s *State) tickMCPDialogExit(now time.Time) {
	if p := s.LocalPanel; p != nil {
		for key, at := range p.mcpExitTimes {
			if !now.Before(at.Add(doublePressWindow)) {
				delete(p.mcpExitTimes, key)
				p.mcpExitPending = ""
			}
		}
	}
}
