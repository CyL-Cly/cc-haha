package teamspawn

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// TeammateTurnResult is the latest assistant text after the real prompt. A
// successfully delivered SendMessage to the lead suppresses duplicate reporting.
func TeammateTurnResult(history []protocol.JSONObject) (string, bool, error) {
	raw, found, err := TeammateTurnResultRawJSON(history)
	var text string
	if raw != nil {
		_ = json.Unmarshal(raw, &text)
	}
	return text, found, err
}

func TeammateTurnResultRawJSON(history []protocol.JSONObject) (json.RawMessage, bool, error) {
	boundary := -1
	for i := len(history) - 1; i >= 0; i-- {
		row := history[i]
		if row.String("type") != "user" || string(row.Get("isMeta")) == "true" {
			continue
		}
		message, _ := row.Object("message")
		content := message.Get("content")
		if len(content) > 0 && content[0] == '"' {
			boundary = i
			break
		}
		blocks, err := originalBlocks(row)
		if err != nil {
			return nil, false, err
		}
		result := false
		for _, raw := range blocks {
			if inboxObject(raw).String("type") == "tool_result" {
				result = true
				break
			}
		}
		if !result {
			boundary = i
			break
		}
	}
	turn := history[boundary+1:]
	delivered := map[string]bool{}
	for _, row := range turn {
		if row.String("type") != "user" {
			continue
		}
		blocks, err := originalBlocks(row)
		if err != nil {
			return nil, false, err
		}
		for _, raw := range blocks {
			block := inboxObject(raw)
			if block.String("type") == "tool_result" && string(block.Get("is_error")) != "true" && !reportsFailedToolResult(block.Get("content")) {
				delivered[block.String("tool_use_id")] = true
			}
		}
	}
	for i := len(turn) - 1; i >= 0; i-- {
		row := turn[i]
		if row.String("type") != "assistant" || string(row.Get("isApiErrorMessage")) == "true" {
			continue
		}
		blocks, err := originalBlocks(row)
		if err != nil {
			return nil, false, err
		}
		for _, raw := range blocks {
			block := inboxObject(raw)
			input, _ := block.Object("input")
			if block.String("type") == "tool_use" && block.String("name") == "SendMessage" && strings.EqualFold(input.String("to"), "team-lead") && delivered[block.String("id")] {
				return nil, false, nil
			}
		}
		text, err := assistantTextRawJSON(blocks)
		if err != nil {
			return nil, false, err
		}
		if string(text) != `""` {
			return text, true, nil
		}
	}
	return nil, false, nil
}

func assistantText(blocks []json.RawMessage) string {
	raw, _ := assistantTextRawJSON(blocks)
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}
func assistantTextRawJSON(blocks []json.RawMessage) (json.RawMessage, error) {
	units := []uint16{}
	count := 0
	for _, raw := range blocks {
		block := inboxObject(raw)
		if block.String("type") != "text" {
			continue
		}
		value := block.Get("text")
		if len(value) == 0 || string(value) == "null" {
			value = []byte(`""`)
		} else if value[0] != '"' {
			var err error
			value, err = protocol.JavaScriptStringJSON(value)
			if err != nil {
				return nil, err
			}
		}
		next, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			units = append(units, '\n')
		}
		count++
		units = append(units, next...)
	}
	return protocol.QuoteJavaScriptUTF16(trimTeammateUTF16(units)), nil
}
func trimTeammateUTF16(units []uint16) []uint16 {
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return units
}
func reportsFailedToolResult(content json.RawMessage) bool {
	texts := []string{}
	if len(content) > 0 && content[0] == '"' {
		var text string
		_ = json.Unmarshal(content, &text)
		texts = append(texts, text)
	} else {
		var blocks []json.RawMessage
		_ = json.Unmarshal(content, &blocks)
		for _, raw := range blocks {
			block := inboxObject(raw)
			if block.String("type") == "text" {
				texts = append(texts, block.String("text"))
			}
		}
	}
	for _, text := range texts {
		if strings.HasPrefix(text, "{") && string(inboxObject([]byte(text)).Get("success")) == "false" {
			return true
		}
	}
	return false
}
