package tui

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// Source useTypeahead shows static argumentHint only after the first space;
// subsequent trailing spaces show unfilled named args using the same shell
// argument parser as the actual skill producer.
func (s *State) commandArgumentHint(input string) string {
	if s.Mode != "prompt" || !strings.HasPrefix(input, "/") || s.Editor.Cursor() == 0 {
		return ""
	}
	space := strings.IndexByte(input, ' ')
	if space < 0 {
		return ""
	}
	name := input[1:space]
	for _, command := range s.commands {
		if command.Name != name {
			continue
		}
		if input == "/"+name+" " && command.ArgumentHint != "" {
			return command.ArgumentHint
		}
		if command.Kind == "prompt" && len(command.ArgumentNames) > 0 && strings.HasSuffix(input, " ") {
			count := len(skillcommands.ParseArguments(input[space+1:]))
			remaining := command.ArgumentNames[min(count, len(command.ArgumentNames)):]
			parts := make([]string, len(remaining))
			for i, name := range remaining {
				parts[i] = "[" + name + "]"
			}
			return strings.Join(parts, " ")
		}
		return ""
	}
	return ""
}
