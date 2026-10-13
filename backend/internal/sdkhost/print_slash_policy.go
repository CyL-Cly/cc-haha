package sdkhost

// ModelSkillCommands retains the global command catalog used by SkillTool.
// Source --disable-slash-commands only clears the user input command list; it
// does not change SkillTool's separate getCommands producer.
func (s *PrintRegistry) ModelSkillCommands() []ControlCommand {
	return projectControlCommands(s.commands)
}
func (s *PrintRegistry) SlashCommandsDisabled() bool { return s != nil && s.slashCommandsDisabled }
