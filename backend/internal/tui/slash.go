package tui

import "strings"

type ParsedSlashCommand struct {
	CommandName string `json:"commandName"`
	Args        string `json:"args"`
	IsMCP       bool   `json:"isMcp"`
}

// ParseSlashCommand is the Source ASCII-space parser. Tabs and repeated spaces
// are significant; tokenizing with Fields would change command arguments.
func ParseSlashCommand(input string) *ParsedSlashCommand {
	input = trimJS(input)
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	words := strings.Split(input[1:], " ")
	if words[0] == "" {
		return nil
	}
	result := &ParsedSlashCommand{CommandName: words[0]}
	start := 1
	if len(words) > 1 && words[1] == "(MCP)" {
		result.CommandName += " (MCP)"
		result.IsMCP = true
		start = 2
	}
	result.Args = strings.Join(words[start:], " ")
	return result
}
