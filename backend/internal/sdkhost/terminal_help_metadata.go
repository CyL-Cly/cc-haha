package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strconv"
)

// TerminalHelpCommands projects the mounted definitions without rereading
// plugin files or replacing their captured prompt closures.
func (s *PrintRegistry) TerminalHelpCommands() []protocol.JSONObject {
	if s == nil || s.slashCommandsDisabled {
		return nil
	}
	builtin := map[string]bool{}
	for _, c := range compiledPrintCommandDeclarations() {
		builtin[c.name] = true
		for _, alias := range c.aliases {
			builtin[alias] = true
		}
	}
	for _, c := range s.commands {
		if c.kind != "prompt" {
			builtin[c.name] = true
			for _, alias := range c.aliases {
				builtin[alias] = true
			}
		}
	}
	rows := []protocol.JSONObject{}
	for _, c := range s.commands {
		name := commandStringJSON(c.rawName, c.name)
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "description", Value: controlCommandDescriptionJSON(c)}, protocol.JSONField{Name: "builtin", Value: json.RawMessage(strconv.FormatBool(builtin[c.name]))}, protocol.JSONField{Name: "hidden", Value: json.RawMessage(strconv.FormatBool(c.hidden))}))
	}
	return rows
}
