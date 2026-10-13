package teamspawn

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

const TeammateSystemPromptAddendum = "\n# Agent Teammate Communication\n\nIMPORTANT: You are running as an agent in a team. To communicate with anyone on your team:\n- Use the SendMessage tool with `to: \"<name>\"` to send messages to specific teammates\n- Use the SendMessage tool with `to: \"*\"` sparingly for team-wide broadcasts\n\nJust writing a response in text is not visible to others on your team - you MUST use the SendMessage tool.\n\nThe user interacts primarily with the team lead. Your work is coordinated through the task system and teammate messaging.\n"

// SystemPrompt composes the original runner body from the actual main prompt
// producer. Alias/model/env sections remain the caller's existing producer.
func SystemPrompt(base []json.RawMessage, custom, override json.RawMessage, mode string) (json.RawMessage, error) {
	if mode == "replace" && len(override) > 0 && string(override) != `""` {
		return append(json.RawMessage(nil), override...), nil
	}
	parts := append([]json.RawMessage{}, base...)
	parts = append(parts, quoted(TeammateSystemPromptAddendum))
	if len(custom) > 0 && string(custom) != `""` {
		value, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("\n# Custom Agent Instructions\n"), custom}, "")
		if err != nil {
			return nil, err
		}
		parts = append(parts, value)
	}
	if mode == "append" && len(override) > 0 && string(override) != `""` {
		parts = append(parts, override)
	}
	return protocol.JoinJavaScriptValuesJSON(parts, "\n")
}

func TeammateMessage(from, content, color, summary string) string {
	attributes := ""
	if color != "" {
		attributes += " color=\"" + teamstore.EscapeXMLAttr(color) + "\""
	}
	if summary != "" {
		attributes += " summary=\"" + teamstore.EscapeXMLAttr(summary) + "\""
	}
	return "<teammate-message teammate_id=\"" + teamstore.EscapeXMLAttr(from) + "\"" + attributes + ">\n" + content + "\n</teammate-message>"
}
