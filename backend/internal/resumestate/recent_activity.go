package resumestate

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var recentSkipPrompt = regexp.MustCompile(`^(?:<[a-z][\w-]*[\s>]|\[Request interrupted by user[^\]]*\])`)
var recentCommandTag = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
var recentCommandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
var recentBashInput = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)

func recentFirstPrompt(head string, builtins map[string]bool) json.RawMessage {
	fallback := ""
	for _, line := range strings.Split(head, "\n") {
		if !strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`) || strings.Contains(line, `"tool_result"`) || strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) {
			continue
		}
		var entry protocol.JSONObject
		if entry.UnmarshalJSON([]byte(line)) != nil || entry.String("type") != "user" {
			continue
		}
		message, _ := entry.Object("message")
		texts := []json.RawMessage{}
		if _, err := protocol.DecodeJavaScriptJSONString(message.Get("content")); err == nil {
			texts = append(texts, message.Get("content"))
		} else {
			var blocks []protocol.JSONObject
			if json.Unmarshal(message.Get("content"), &blocks) == nil {
				for _, block := range blocks {
					if block.String("type") == "text" {
						texts = append(texts, block.Get("text"))
					}
				}
			}
		}
		for _, raw := range texts {
			units, err := protocol.DecodeJavaScriptJSONString(raw)
			if err != nil || len(units) == 0 {
				continue
			}
			for i := range units {
				if units[i] == '\n' {
					units[i] = ' '
				}
			}
			result := protocol.TrimJavaScriptString(text(protocol.QuoteJavaScriptUTF16(units)))
			if match := recentCommandTag.FindStringSubmatch(result); len(match) > 1 {
				args := ""
				if match := recentCommandArgs.FindStringSubmatch(result); len(match) > 1 {
					args = protocol.TrimJavaScriptString(match[1])
				}
				if builtins[strings.TrimPrefix(match[1], "/")] || args == "" {
					if fallback == "" {
						fallback = match[1]
					}
					continue
				}
				return quote(match[1] + " " + args)
			}
			if match := recentBashInput.FindStringSubmatch(result); len(match) > 1 {
				return quote("! " + match[1])
			}
			if recentSkipPrompt.MatchString(result) {
				continue
			}
			trimmed, _ := protocol.DecodeJavaScriptJSONString(quote(result))
			if len(trimmed) > 200 {
				return quote(protocol.TrimJavaScriptString(text(protocol.QuoteJavaScriptUTF16(trimmed[:200]))) + "…")
			}
			return quote(result)
		}
	}
	if fallback != "" {
		return quote(fallback)
	}
	return nil
}

func recentPrefix(head, key string) string {
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		index := strings.Index(head, pattern)
		if index < 0 {
			continue
		}
		units, _ := protocol.DecodeJavaScriptJSONString(quote(head[index+len(pattern):]))
		end, count := 0, 0
		for end < len(units) && count < 200 {
			if units[end] == '\\' {
				end += 2
			} else if units[end] == '"' {
				break
			} else {
				end++
			}
			count++
		}
		value := text(protocol.QuoteJavaScriptUTF16(units[:min(end, len(units))]))
		return protocol.TrimJavaScriptString(strings.ReplaceAll(strings.ReplaceAll(value, `\n`, " "), `\t`, " "))
	}
	return ""
}

// ReadRecentActivity preserves fetchLogs(10)'s stat limit before bounded reads,
// followed by enrichLogs' sidechain/team filters and Logo's current/Summary
// filters. It uses the existing portable head/tail native failure boundary.
func ReadRecentActivity(files ContinueFiles, directory, currentSID string, statFile func(string) (Stat, error), builtins map[string]bool) []protocol.JSONObject {
	entries, err := files.ReadDir(directory)
	if err != nil {
		return []protocol.JSONObject{}
	}
	values := []candidate{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !uuidName.MatchString(sid) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if stat, err := statFile(path); err == nil {
			values = append(values, candidate{sid: sid, path: path, stat: stat})
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].stat.Modified > values[j].stat.Modified })
	values = values[:min(10, len(values))]
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].stat.Modified != values[j].stat.Modified {
			return values[i].stat.Modified > values[j].stat.Modified
		}
		return values[i].stat.Birth > values[j].stat.Birth
	})
	logs := []protocol.JSONObject{}
	for _, value := range values {
		head, tail := ReadHeadAndTail(files, value.path, value.stat.Size)
		if strings.Contains(head, `"isSidechain":true`) || strings.Contains(head, `"isSidechain": true`) || truthy(extract(head, "teamName", false)) || value.sid == currentSID {
			continue
		}
		first := extract(tail, "lastPrompt", true)
		if !truthy(first) {
			first = recentFirstPrompt(head, builtins)
		}
		if !truthy(first) {
			first = quote(recentPrefix(head, "content"))
		}
		if !truthy(first) {
			first = quote(recentPrefix(head, "text"))
		}
		title := extract(tail, "customTitle", true)
		if title == nil {
			title = extract(head, "customTitle", true)
		}
		if title == nil {
			title = extract(tail, "aiTitle", true)
		}
		if title == nil {
			title = extract(head, "aiTitle", true)
		}
		if !truthy(first) && !truthy(title) {
			first = quote("(session)")
		}
		summary := extract(tail, "summary", true)
		if strings.Contains(text(summary), "I apologize") || (!truthy(summary) || text(summary) == "No prompt") && (!truthy(first) || text(first) == "No prompt") {
			continue
		}
		logs = append(logs, protocol.NewJSONObject(protocol.JSONField{Name: "firstPrompt", Value: first}, protocol.JSONField{Name: "summary", Value: summary}, protocol.JSONField{Name: "modified", Value: numeric(value.stat.Modified)}, protocol.JSONField{Name: "sessionId", Value: quote(value.sid)}))
	}
	return logs[:min(3, len(logs))]
}
