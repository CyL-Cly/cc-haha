package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func stripResumeTags(value string, allowEmpty bool) string {
	var out strings.Builder
	cursor := 0
	for cursor < len(value) {
		relative := strings.IndexByte(value[cursor:], '<')
		if relative < 0 {
			out.WriteString(value[cursor:])
			break
		}
		start := cursor + relative
		out.WriteString(value[cursor:start])
		end := start + 1
		if end >= len(value) || value[end] < 'a' || value[end] > 'z' {
			out.WriteByte('<')
			cursor = end
			continue
		}
		end++
		for end < len(value) && (value[end] >= 'a' && value[end] <= 'z' || value[end] >= 'A' && value[end] <= 'Z' || value[end] >= '0' && value[end] <= '9' || value[end] == '_' || value[end] == '-') {
			end++
		}
		name := value[start+1 : end]
		if end < len(value) && value[end] != '>' {
			r, _ := utf8.DecodeRuneInString(value[end:])
			if !protocol.IsJavaScriptWhitespace(r) {
				out.WriteByte('<')
				cursor = start + 1
				continue
			}
			at := strings.IndexByte(value[end:], '>')
			if at < 0 {
				out.WriteString(value[start:])
				break
			}
			end += at
		}
		if end >= len(value) {
			out.WriteString(value[start:])
			break
		}
		end++
		closing := "</" + name + ">"
		at := strings.Index(value[end:], closing)
		if at < 0 {
			out.WriteByte('<')
			cursor = start + 1
			continue
		}
		cursor = end + at + len(closing)
		if cursor < len(value) && value[cursor] == '\n' {
			cursor++
		}
	}
	result := protocol.TrimJavaScriptString(out.String())
	if result == "" && !allowEmpty {
		return value
	}
	return result
}
func resumeSourceDisplayTitle(log protocol.JSONObject) string {
	first := log.String("firstPrompt")
	autonomous := strings.HasPrefix(first, "<tick>")
	stripped := stripResumeTags(first, true)
	title := ""
	for _, key := range []string{"agentName", "customTitle", "summary"} {
		if v := log.String(key); v != "" {
			title = v
			break
		}
	}
	if title == "" && stripped != "" && !autonomous {
		title = stripped
	}
	if title == "" && autonomous {
		title = "Autonomous session"
	}
	if title == "" {
		sid := []rune(log.String("sessionId"))
		title = string(sid[:min(8, len(sid))])
	}
	return protocol.TrimJavaScriptString(stripResumeTags(title, false))
}
