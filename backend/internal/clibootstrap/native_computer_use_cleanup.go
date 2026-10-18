package clibootstrap

// BeginNativeComputerUseCleanupClose precedes the current-turn/native join.
// A child has its own Host and cannot cancel the main host's cleanup lifetime.
func (m *MainPrint) BeginNativeComputerUseCleanupClose() {
	if m != nil && m.Host != nil {
		m.Host.BeginComputerUseCleanupClose()
	}
	if m != nil && m.computerUse != nil && !m.agentChild && m.computerUse.beginClose != nil {
		m.computerUse.beginClose()
	}
}
