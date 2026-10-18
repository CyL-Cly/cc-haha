package tui

func (s *State) terminalLocalUIActive() bool {
	return s.LocalPanel != nil || s.Doctor != nil || s.ModelPicker != nil || s.IDEPicker != nil || s.ThinkingPicker != nil
}
func (m *terminalModel) publishTerminalLocalUI() {
	if source, ok := m.session.(LocalUIStateSession); ok {
		source.SetTerminalLocalUIActive(m.state.terminalLocalUIActive())
	}
}
