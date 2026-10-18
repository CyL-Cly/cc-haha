package clibootstrap

func (m *MainPrint) NativeUsageCommandEnabled() bool {
	return m.nativeModelSelector.ClaudeAISubscriberAvailable()
}
