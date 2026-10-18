package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// TerminalSkillMetadata mirrors SkillsMenu's loadedFrom predicate. It returns
// detached metadata from the execution registry, including model-only skills.
func (s *PrintRegistry) TerminalSkillMetadata() []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	for _, command := range s.prompts {
		metadata := command.Metadata()
		switch metadata.String("loadedFrom") {
		case "skills", "commands_DEPRECATED", "plugin", "mcp":
			rows = append(rows, metadata)
		}
	}
	return rows
}
