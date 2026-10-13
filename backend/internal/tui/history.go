package tui

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SetInitialHistory consumes the original REPL initialMessages snapshot once.
// Retained tool inputs/results remain independent copies for transcript view;
// no historical envelope is sent through live-turn or permission consumers.
func (s *State) SetInitialHistory(records []protocol.JSONObject, fullscreen bool) {
	s.Entries = nil
	if !fullscreen {
		for i := len(records) - 1; i >= 0; i-- {
			if records[i].String("type") == "system" && records[i].String("subtype") == "compact_boundary" {
				records = records[i:]
				break
			}
		}
	}
	tools := map[string]int{}
	for _, record := range records {
		id := record.String("uuid")
		kind := record.String("type")
		if kind == "system" {
			if text := record.String("content"); text != "" && record.String("subtype") != "microcompact_boundary" {
				s.Entries = append(s.Entries, Entry{TurnID: id, Kind: "system", Text: text, Level: record.String("level")})
			}
			continue
		}
		if kind != "user" && kind != "assistant" || kind == "user" && string(record.Get("isMeta")) == "true" {
			continue
		}
		content := presentationObject(record.Get("message")).Get("content")
		var blocks []protocol.JSONObject
		if _, ok := presentationString(content); ok {
			blocks = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"text"`)}, protocol.JSONField{Name: "text", Value: content})}
		} else {
			_ = json.Unmarshal(content, &blocks)
		}
		for _, block := range blocks {
			entry := Entry{TurnID: id, Kind: kind, TranscriptOnly: kind == "user" && string(record.Get("isVisibleInTranscriptOnly")) == "true"}
			switch block.String("type") {
			case "text":
				entry.Text = block.String("text")
				if protocol.TrimJavaScriptString(entry.Text) == "" || entry.Text == "(no content)" || entry.Text == "[Request interrupted by user for tool use]" {
					continue
				}
				if kind == "user" {
					entry.Kind, entry.Text = historicalUserText(entry.Text)
					if entry.Text == "" {
						continue
					}
				}
			case "image":
				if kind != "user" {
					continue
				}
				entry.Text = "[Image data]"
			case "thinking":
				entry.Kind, entry.Text, entry.TranscriptOnly = "thinking", block.String("thinking"), true
			case "redacted_thinking":
				entry.Kind, entry.Text, entry.TranscriptOnly = "thinking", "[Redacted thinking]", true
				entry.Redacted = true
			case "tool_use":
				entry.Kind, entry.ToolID, entry.ToolName = "tool", block.String("id"), block.String("name")
				entry.Input = append(json.RawMessage(nil), block.Get("input")...)
				entry.Text = toolInputPresentation(entry.ToolName, entry.Input)
				tools[entry.ToolID] = len(s.Entries)
			case "tool_result":
				index, exists := tools[block.String("tool_use_id")]
				if !exists {
					continue
				}
				tool := &s.Entries[index]
				tool.Output = append(json.RawMessage(nil), block.Get("content")...)
				tool.Result = append(json.RawMessage(nil), record.Get("toolUseResult")...)
				tool.IsError = string(block.Get("is_error")) == "true"
				tool.Text += "\n" + toolResultPresentation(*tool, false)
				continue
			default:
				continue
			}
			s.Entries = append(s.Entries, entry)
		}
	}
}

func historicalUserText(text string) (string, string) {
	if strings.Contains(text, "<local-command-caveat>") || strings.Contains(text, "<tick>") {
		return "", ""
	}
	for _, pair := range []struct{ tag, kind string }{{"local-command-stdout", "local_output"}, {"local-command-stderr", "local_output"}, {"bash-stdout", "bash_output"}, {"bash-stderr", "bash_output"}, {"bash-input", "user"}} {
		if pair.tag == "bash-input" && !strings.Contains(text, "<bash-input>") || pair.tag != "bash-input" && !strings.HasPrefix(text, "<"+pair.tag) {
			continue
		}
		var outputs []string
		for _, tag := range []string{pair.tag, strings.Replace(pair.tag, "stdout", "stderr", 1)} {
			start := strings.Index(text, "<"+tag+">")
			if start < 0 {
				continue
			}
			tail := text[start+len(tag)+2:]
			if end := strings.Index(tail, "</"+tag+">"); end >= 0 {
				value := strings.TrimSpace(tail[:end])
				if len(outputs) == 0 || outputs[len(outputs)-1] != value {
					outputs = append(outputs, value)
				}
			}
		}
		return pair.kind, strings.Join(outputs, "\n")
	}
	return "user", text
}
