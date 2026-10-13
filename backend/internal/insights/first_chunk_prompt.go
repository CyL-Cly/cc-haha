package insights

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FirstChunkPrompt is the lite picker selector, which differs from the full
// transcript selector by retaining the first slash command as a fallback.
func FirstChunkPrompt(chunk string, builtins map[string]bool) json.RawMessage {
	var commandFallback []uint16
	for _, line := range strings.Split(chunk, "\n") {
		if (!strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`)) || strings.Contains(line, `"tool_result"`) || strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) {
			continue
		}
		var entry protocol.JSONObject
		if entry.UnmarshalJSON([]byte(line)) != nil || entry.String("type") != "user" {
			continue
		}
		body, _ := entry.Object("message")
		content := body.Get("content")
		texts := []json.RawMessage{}
		if len(content) > 0 && content[0] == '"' {
			texts = append(texts, content)
		} else {
			var blocks []json.RawMessage
			if json.Unmarshal(content, &blocks) != nil {
				continue
			}
			invalid := false
			for _, block := range blocks {
				var b protocol.JSONObject
				if string(block) == "null" {
					invalid = true
					break
				}
				if b.UnmarshalJSON(block) == nil && b.String("type") == "text" && len(b.Get("text")) > 0 && b.Get("text")[0] == '"' {
					texts = append(texts, b.Get("text"))
				}
			}
			if invalid {
				continue
			}
		}
		for _, text := range texts {
			units, e := protocol.DecodeJavaScriptJSONString(text)
			if e != nil || len(units) == 0 {
				continue
			}
			for i, u := range units {
				if u == '\n' {
					units[i] = ' '
				}
			}
			units = trimUnits(units)
			if name := extractTag(units, "command-name"); len(name) > 0 {
				plain := name
				if plain[0] == '/' {
					plain = plain[1:]
				}
				args := trimUnits(extractTag(units, "command-args"))
				if builtins[string(utf16.Decode(plain))] || len(args) == 0 {
					if len(commandFallback) == 0 {
						commandFallback = append([]uint16{}, name...)
					}
					continue
				}
				return protocol.QuoteJavaScriptUTF16(append(append(append([]uint16{}, name...), ' '), args...))
			}
			if bash := extractTag(units, "bash-input"); len(bash) > 0 {
				return protocol.QuoteJavaScriptUTF16(append([]uint16{'!', ' '}, bash...))
			}
			if skipPrompt(units) {
				continue
			}
			if len(units) > 200 {
				units = append(trimUnits(units[:200]), 0x2026)
			}
			return protocol.QuoteJavaScriptUTF16(units)
		}
	}
	return protocol.QuoteJavaScriptUTF16(commandFallback)
}
