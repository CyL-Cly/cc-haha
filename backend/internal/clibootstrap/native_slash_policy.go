package clibootstrap

func (m *MainPrint) SlashCommandsDisabled() bool {
	return m != nil && m.options.Arguments.DisableSlashCommands
}
