package sdkhost

import "sort"

// SDKInitializeCommands restores COMMANDS() declaration order after native
// executable prompt discovery. Bundled, plugin and user prompts retain their
// actual discovery order; ordinary tool guidance retains its existing catalog.
func (s *PrintRegistry) SDKInitializeCommands() []ControlCommand {
	order := map[string]int{}
	for i, name := range []string{"agent", "compact", "context", "cost", "heapdump", "init", "pr-comments", "release-notes", "goal", "review", "security-review", "insights"} {
		order[name] = i
	}
	external, builtin := []printCommandDefinition{}, []printCommandDefinition{}
	seen := map[string]bool{}
	for _, command := range s.commands {
		_, authored := order[command.name]
		if authored && (command.source == "builtin" || command.source == "" || command.kind != "prompt") {
			if !seen[command.name] {
				builtin = append(builtin, command)
				seen[command.name] = true
			}
		} else {
			external = append(external, command)
		}
	}
	sort.SliceStable(builtin, func(i, j int) bool {
		a, okA := order[builtin[i].name]
		b, okB := order[builtin[j].name]
		if !okA || !okB {
			return okA && !okB
		}
		return a < b
	})
	return projectControlCommands(append(external, builtin...))
}
