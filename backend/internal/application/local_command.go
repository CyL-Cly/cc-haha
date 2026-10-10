package application

import (
	"encoding/json"
	"slices"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These parsers follow cliMessageParsing's string/array extraction, tag
// precedence and ECMAScript trim while preserving JSON's UTF-16 strings.
func localCommandText(content json.RawMessage) []uint16 {
	if units, err := protocol.DecodeJavaScriptJSONString(content); err == nil {
		return units
	}
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var text []uint16
	count := 0
	for _, rawBlock := range blocks {
		block := obj(rawBlock)
		units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
		if err != nil || !block.IsObject() {
			continue
		}
		if count > 0 {
			text = append(text, '\n')
		}
		text = append(text, units...)
		count++
	}
	return text
}

func localCommandSpace(c uint16) bool {
	return c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' || c == ' ' || c == 0xa0 || c == 0x1680 || c >= 0x2000 && c <= 0x200a || c == 0x2028 || c == 0x2029 || c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff
}

func trimLocalCommand(units []uint16) []uint16 {
	for len(units) > 0 && localCommandSpace(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && localCommandSpace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return units
}

func localTag(units []uint16, name string) ([]uint16, bool) {
	open, close := utf16.Encode([]rune("<"+name+">")), utf16.Encode([]rune("</"+name+">"))
	find := func(text, pattern []uint16) int {
		for i := 0; i+len(pattern) <= len(text); i++ {
			if slices.Equal(text[i:i+len(pattern)], pattern) {
				return i
			}
		}
		return -1
	}
	start := find(units, open)
	if start < 0 {
		return nil, false
	}
	tail := units[start+len(open):]
	end := find(tail, close)
	if end < 0 {
		return nil, false
	}
	return trimLocalCommand(tail[:end]), true
}

type localCommand struct{ name []uint16 }

func parseLocalCommand(content json.RawMessage) *localCommand {
	name, _ := localTag(localCommandText(content), "command-name")
	if len(name) == 0 {
		return nil
	}
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}
	return &localCommand{name: name}
}

func localCommandOutput(content json.RawMessage, untagged bool) json.RawMessage {
	text := localCommandText(content)
	if len(text) == 0 {
		return nil
	}
	for _, tag := range []string{"local-command-stdout", "local-command-stderr"} {
		if output, found := localTag(text, tag); found {
			return protocol.QuoteJavaScriptUTF16(output)
		}
	}
	if untagged {
		if output := trimLocalCommand(text); len(output) > 0 {
			return protocol.QuoteJavaScriptUTF16(output)
		}
	}
	return nil
}

func localGoalEvent(output json.RawMessage, command *localCommand) json.RawMessage {
	isName := func(name string) bool {
		return command != nil && slices.Equal(command.name, utf16.Encode([]rune(name)))
	}
	if command != nil && !isName("goal") {
		return nil
	}
	units, err := protocol.DecodeJavaScriptJSONString(output)
	if err != nil {
		return nil
	}
	units = trimLocalCommand(units)
	if len(units) == 0 {
		return nil
	}
	equals := func(text string) bool { return slices.Equal(units, utf16.Encode([]rune(text))) }
	prefix := func(text string) bool {
		pattern := utf16.Encode([]rune(text))
		return len(units) >= len(pattern) && slices.Equal(units[:len(pattern)], pattern)
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "action", Value: nil})
	action := ""
	switch {
	case equals("Goal cleared.") || prefix("Goal cleared:"):
		action = "cleared"
	case equals("Goal marked complete."):
		action = "completed"
	case equals("No active goal."):
		action = "message"
	case prefix("Goal continuing:"):
		action = "status"
		event.Set("status", raw("continuing"))
	case prefix("Goal set:"):
		action = "created"
		event.Set("status", raw("active"))
		objective := trimLocalCommand(units[len("Goal set:"):])
		if len(objective) > 0 {
			event.Set("objective", protocol.QuoteJavaScriptUTF16(objective))
		}
	case isName("goal"):
		action = "message"
	default:
		return nil
	}
	event.Set("action", raw(action))
	event.Set("message", protocol.QuoteJavaScriptUTF16(units))
	return raw(event)
}

func (p *Projection) localOutput(output json.RawMessage) []json.RawMessage {
	goal := localGoalEvent(output, p.localCommand)
	p.localCommand = nil
	if goal != nil {
		return []json.RawMessage{frame("system_notification", field("subtype", "goal_event"), protocol.JSONField{Name: "message", Value: obj(goal).Get("message")}, protocol.JSONField{Name: "data", Value: goal})}
	}
	return []json.RawMessage{frame("content_start", field("blockType", "text")), frame("content_delta", protocol.JSONField{Name: "text", Value: output})}
}
