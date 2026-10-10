package teamspawn

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func originalBlocks(value protocol.JSONObject) ([]json.RawMessage, error) {
	message, ok := value.Object("message")
	if !ok {
		return nil, nil
	}
	content := bytes.TrimSpace(message.Get("content"))
	if len(content) == 0 || content[0] != '[' {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(content, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if bytes.Equal(bytes.TrimSpace(row), []byte("null")) {
			return nil, errors.New("null is not an object (evaluating 'block.type')")
		}
	}
	return rows, nil
}

func LastPeerDMSummary(messages []protocol.JSONObject) (json.RawMessage, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		api, _ := message.Object("message")
		if message.String("type") == "user" {
			if _, err := protocol.DecodeJavaScriptJSONString(api.Get("content")); err == nil {
				break
			}
		}
		if message.String("type") != "assistant" {
			continue
		}
		blocks, err := originalBlocks(message)
		if err != nil {
			return nil, err
		}
		for _, raw := range blocks {
			block := inboxObject(raw)
			input, ok := block.Object("input")
			if block.String("type") != "tool_use" || block.String("name") != "SendMessage" || !ok {
				continue
			}
			if _, err := protocol.DecodeJavaScriptJSONString(input.Get("to")); err != nil {
				continue
			}
			to := input.String("to")
			if to == "*" || strings.EqualFold(to, "team-lead") {
				continue
			}
			units, err := protocol.DecodeJavaScriptJSONString(input.Get("message"))
			if err != nil {
				continue
			}
			summary := input.Get("summary")
			if _, err := protocol.DecodeJavaScriptJSONString(summary); err != nil {
				if len(units) > 80 {
					units = units[:80]
				}
				summary = protocol.QuoteJavaScriptUTF16(units)
			}
			return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("[to "), input.Get("to"), quoted("] "), summary}, "")
		}
	}
	return nil, nil
}

// FilterIncompleteContext is runAgent's fork filter. An incomplete tool_use
// discards the whole assistant envelope, including its text/thinking blocks.
// Other records retain their original position and contents.
func FilterIncompleteContext(messages []protocol.JSONObject) ([]protocol.JSONObject, error) {
	results := map[string]bool{}
	for _, message := range messages {
		if message.String("type") != "user" {
			continue
		}
		blocks, err := originalBlocks(message)
		if err != nil {
			return nil, err
		}
		for _, raw := range blocks {
			block := inboxObject(raw)
			id := block.Get("tool_use_id")
			if block.String("type") == "tool_result" && inboxTruthy(id) {
				results[string(protocol.StringifyValidatedJavaScriptJSON(id))] = true
			}
		}
	}
	out := make([]protocol.JSONObject, 0, len(messages))
	for _, message := range messages {
		incomplete := false
		if message.String("type") == "assistant" {
			blocks, err := originalBlocks(message)
			if err != nil {
				return nil, err
			}
			for _, raw := range blocks {
				block := inboxObject(raw)
				id := block.Get("id")
				if block.String("type") == "tool_use" && inboxTruthy(id) && !results[string(protocol.StringifyValidatedJavaScriptJSON(id))] {
					incomplete = true
					break
				}
			}
		}
		if !incomplete {
			out = append(out, message)
		}
	}
	return out, nil
}
