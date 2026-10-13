// Package trajectory projects bounded physical transcript records into the
// desktop ledger independently of the chat message projection.
package trajectory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type Object = map[string]any
type Row = map[string]any
type Entry struct {
	Value      Object
	Start, End int64
	Oversized  int64
}

func object(v any) Object {
	x, _ := v.(map[string]any)
	if x == nil {
		return Object{}
	}
	return x
}
func text(v any) string    { x, _ := v.(string); return x }
func array(v any) []any    { x, _ := v.([]any); return x }
func number(v any) float64 { x, _ := v.(float64); return x }
func first(v ...any) string {
	for _, x := range v {
		if s := text(x); s != "" {
			return s
		}
	}
	return ""
}
func raw(v any) []byte      { b, _ := json.Marshal(v); return b }
func parse(b []byte) Object { var v Object; _ = json.Unmarshal(b, &v); return v }
func chars(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	return string(utf16.Decode(u[:max(0, n)]))
}
func Preview(s string, n int) string {
	s = strings.Join(strings.Fields(chars(s, n*4)), " ")
	if len(utf16.Encode([]rune(s))) > n {
		s = chars(s, n-1) + "…"
	}
	return s
}
func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	out := []string{}
	for _, b := range array(v) {
		x := object(b)
		if x["type"] == "text" {
			out = append(out, text(x["text"]))
		}
	}
	return strings.Join(out, "\n")
}
func contentPreview(v any, n int) string {
	if s, ok := v.(string); ok {
		return Preview(s, n)
	}
	parts := []string{}
	length := 0
	for _, b := range array(v) {
		if length > n {
			break
		}
		x := object(b)
		part := ""
		switch x["type"] {
		case "text":
			part = text(x["text"])
		case "image":
			part = "[image]"
		case "document":
			part = "[document]"
		case "tool_result":
			part = contentPreview(x["content"], n)
		}
		if part != "" {
			part = chars(part, n*4)
			parts = append(parts, part)
			length += len(part)
		}
	}
	return Preview(strings.Join(parts, " "), n)
}

var markdownLink = regexp.MustCompile(`!?\[([^\]\n]*)\]\([^\)\n]*\)`)
var markdownPrefix = regexp.MustCompile(`(?m)^[ \t]*(?:#{1,6}[ \t]+|(?:>[ \t]?)+|[-*+][ \t]+|\d{1,3}[.)][ \t]+)`)
var markdownInline = regexp.MustCompile("\\*\\*|__|`+")
var commandTags = regexp.MustCompile(`</?(?:local-command-stdout|local-command-stderr|command-name|command-message|command-args)>`)
var commandName = regexp.MustCompile(`<command-name>([\s\S]*?)</command-name>`)
var commandArgs = regexp.MustCompile(`<command-args>([\s\S]*?)</command-args>`)
var agentIDPattern = regexp.MustCompile(`(?:^|\n)\s*(?:agentId|agent_id):\s*([A-Za-z0-9_@.-]+)`)
var oversizedType = regexp.MustCompile(`"type":"(user|assistant|attachment|system)"`)
var oversizedUUID = regexp.MustCompile(`"uuid":"([0-9A-Za-z_-]{1,128})"`)
var oversizedTime = regexp.MustCompile(`"timestamp":"(\d{4}-\d{2}-\d{2}T[0-9:.]{1,20}(?:Z|[+-]\d{2}:?\d{2}))"`)
var oversizedTool = regexp.MustCompile(`"tool_use_id":"([^"\\]{1,256})"`)

func stripMarkdown(s string) string {
	s = markdownLink.ReplaceAllString(chars(s, 2048), "$1")
	s = markdownPrefix.ReplaceAllString(s, "")
	return markdownInline.ReplaceAllString(s, "")
}
func commandText(s string) string {
	s = chars(s, 2048)
	if m := commandName.FindStringSubmatch(s); len(m) > 1 {
		args := ""
		if a := commandArgs.FindStringSubmatch(s); len(a) > 1 {
			args = a[1]
		}
		return strings.TrimSpace(m[1] + " " + args)
	}
	return commandTags.ReplaceAllString(s, " ")
}
func skipped(e Object, main bool) bool {
	t := text(e["type"])
	return (t != "user" && t != "assistant" && t != "attachment" && t != "system") || main && e["isSidechain"] == true
}
func realPrompt(e Object) bool {
	if e["type"] != "user" || e["isCompactSummary"] == true || e["isVisibleInTranscriptOnly"] == true {
		return false
	}
	m := object(e["message"])
	if m["role"] != "user" {
		return false
	}
	s := strings.TrimSpace(contentText(m["content"]))
	if e["isMeta"] == true && collaborationDelivery(m["content"]) == nil {
		return false
	}
	if interruption(m["content"]) || taskNotification(m["content"]) || strings.HasPrefix(s, "<local-command-stdout>") || strings.HasPrefix(s, "<local-command-stderr>") {
		return false
	}
	if _, ok := m["content"].(string); ok {
		return s != ""
	}
	for _, b := range array(m["content"]) {
		if t := object(b)["type"]; t != nil && t != "tool_result" {
			return true
		}
	}
	return false
}
func inputSummary(name string, input any) string {
	v := object(input)
	keys := []string{}
	switch name {
	case "Bash", "PowerShell":
		keys = []string{"command"}
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		keys = []string{"file_path", "notebook_path", "path"}
	case "Grep", "Glob":
		p := text(v["pattern"])
		if where := text(v["path"]); p != "" && where != "" {
			return Preview(p+" in "+where, 200)
		}
		keys = []string{"pattern"}
	case "WebFetch":
		keys = []string{"url"}
	case "WebSearch":
		keys = []string{"query"}
	case "Agent", "Task":
		keys = []string{"description", "subagent_type"}
	case "Skill":
		keys = []string{"skill"}
	}
	keys = append(keys, "command", "file_path", "path", "pattern", "url", "query", "description", "skill", "prompt", "name")
	for _, k := range keys {
		if s := strings.TrimSpace(text(v[k])); s != "" {
			return Preview(s, 200)
		}
	}
	return ""
}
func resultPreview(v any) string {
	text := contentText(v)
	images := 0
	for _, b := range array(v) {
		if object(b)["type"] == "image" {
			images++
		}
	}
	if images == 0 {
		return Preview(first(text, contentPreview(v, 300)), 300)
	}
	markers := strings.TrimSpace(strings.Repeat("[image] ", min(4, images)))
	if images > 4 {
		markers += fmt.Sprintf(" +%d", images-4)
	}
	if text != "" {
		return Preview(text, max(1, 299-len(markers))) + " " + markers
	}
	return markers
}
func usage(v any) Object {
	u := object(v)
	if len(u) == 0 {
		return nil
	}
	return Object{"input": number(u["input_tokens"]), "output": number(u["output_tokens"]), "cacheRead": number(u["cache_read_input_tokens"]), "cacheWrite": number(u["cache_creation_input_tokens"])}
}
func mergeUsage(a, b Object) Object {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	for _, k := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		a[k] = max(number(a[k]), number(b[k]))
	}
	return a
}
func fieldSummary(v Object) string {
	keys := []string{}
	for k := range v {
		if k != "type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys[:min(6, len(keys))] {
		x := v[k]
		switch x.(type) {
		case string:
			parts = append(parts, k+"="+chars(text(x), 60))
		case float64, bool:
			parts = append(parts, fmt.Sprintf("%s=%v", k, x))
		case []any:
			parts = append(parts, fmt.Sprintf("%s[%d]", k, len(array(x))))
		case map[string]any:
			parts = append(parts, k+"{…}")
		}
	}
	return strings.Join(parts, " ")
}

func Oversized(head []byte) Object {
	out := Object{}
	for k, re := range map[string]*regexp.Regexp{"type": oversizedType, "uuid": oversizedUUID, "timestamp": oversizedTime} {
		if m := re.FindSubmatch(head); len(m) > 1 {
			out[k] = string(m[1])
		}
	}
	ids := []any{}
	seen := map[string]bool{}
	for _, m := range oversizedTool.FindAllSubmatch(head, 64) {
		id := string(m[1])
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	out["toolUseIds"] = ids
	return out
}

func Project(entries []Entry, turnBase *int, main bool, preceding []string) []Row {
	rows := []Row{}
	assistants, tools := map[string]Row{}, map[string]Row{}
	turn := 0
	if turnBase != nil {
		turn = *turnBase
	}
	recent := append([]string{}, preceding...)
	projected := false
	push := func(r Row) Row { rows = append(rows, r); return r }
	remember := func(ts string) {
		if ts != "" {
			recent = append(recent, ts)
			if len(recent) > 32 {
				recent = recent[len(recent)-16:]
			}
		}
	}
	patchTool := func(id, ts string, loc [2]int64, patch Row) {
		r := tools[id]
		if r == nil {
			r = push(Row{"id": "t:" + id, "kind": "tool", "turn": turn, "ts": ts, "preview": "", "loc": [][2]int64{loc}, "partial": true, "toolUseId": id})
			tools[id] = r
		} else {
			r["loc"] = append(r["loc"].([][2]int64), loc)
		}
		for k, v := range patch {
			r[k] = v
		}
	}
	for _, item := range entries {
		e := item.Value
		if skipped(e, main) {
			continue
		}
		loc := [2]int64{item.Start, item.End}
		ts := text(e["timestamp"])
		id := first(e["uuid"], fmt.Sprintf("@%d", item.Start))
		prior := projected
		projected = true
		if item.Oversized > 0 {
			if e["type"] == "user" {
				for _, v := range array(e["toolUseIds"]) {
					patchTool(text(v), ts, loc, Row{"resultOmittedBytes": item.Oversized, "resultTs": ts})
				}
			}
			remember(ts)
			continue
		}
		base := func(prefix, kind, preview string) Row {
			return Row{"id": prefix + ":" + id, "kind": kind, "turn": turn, "ts": ts, "preview": preview, "loc": [][2]int64{loc}}
		}
		switch e["type"] {
		case "user":
			m := object(e["message"])
			if len(m) == 0 {
				remember(ts)
				continue
			}
			content := m["content"]
			results := 0
			for _, b := range array(content) {
				v := object(b)
				if v["type"] != "tool_result" {
					continue
				}
				results++
				key := text(v["tool_use_id"])
				if key == "" {
					continue
				}
				patch := Row{"resultPreview": resultPreview(v["content"]), "resultTs": ts}
				if v["is_error"] == true {
					patch["isError"] = true
				}
				agent := text(object(e["toolUseResult"])["agentId"])
				if agent == "" {
					if a := agentIDPattern.FindStringSubmatch(contentText(v["content"])); len(a) > 1 {
						agent = a[1]
					}
				}
				if agent != "" {
					patch["agentId"] = agent
				}
				patchTool(key, ts, loc, patch)
			}
			if results > 0 && results == len(array(content)) {
				remember(ts)
				continue
			}
			if realPrompt(e) {
				turn++
				r := base("u", "user", contentPreview(content, 512))
				r["turn"] = turn
				s := strings.TrimSpace(contentText(content))
				if sender, body, ok := deliveredMessage(content); ok {
					r["label"] = "session_message"
					if sender != "" {
						body = sender + ": " + body
					}
					r["preview"] = Preview(body, 512)
				} else if strings.HasPrefix(s, "<command-name>") {
					r["label"] = "command"
					r["preview"] = Preview(commandText(s), 512)
				} else if strings.HasPrefix(s, "<teammate-message") || strings.HasPrefix(s, "Another Claude session sent a message:") {
					r["label"] = "session_message"
				}
				push(r)
			} else {
				label := "meta"
				s := strings.TrimSpace(contentText(content))
				switch {
				case e["isCompactSummary"] == true:
					label = "summary"
				case e["isMeta"] == true && strings.HasPrefix(s, "Base directory for this skill:"):
					label = "skill"
				case interruption(content):
					label = "interrupted"
				case taskNotification(content):
					label = "task_notification"
				case strings.HasPrefix(s, "<local-command-"):
					label = "command_output"
				}
				r := base("c", "context", contentPreview(content, 512))
				r["label"] = label
				if label == "command_output" {
					r["preview"] = Preview(commandText(s), 512)
				}
				push(r)
			}
		case "assistant":
			m := object(e["message"])
			key := first(m["id"], id)
			blocks := array(m["content"])
			body := ""
			thinking := false
			for _, b := range blocks {
				v := object(b)
				if v["type"] == "text" && body == "" && strings.TrimSpace(text(v["text"])) != "" {
					body = text(v["text"])
				}
				thinking = thinking || v["type"] == "thinking" || v["type"] == "redacted_thinking"
			}
			u := usage(m["usage"])
			inherited := object(e["forkedFrom"])
			if text(inherited["sessionId"]) != "" && text(inherited["messageUuid"]) != "" {
				u = nil
			}
			r := assistants[key]
			if r == nil {
				r = base("a", "assistant", Preview(stripMarkdown(body), 512))
				r["id"] = "a:" + key
				r["messageId"] = key
				r["toolOnly"] = body == ""
				r["hasThinking"] = thinking
				r["endTs"] = ts
				if !prior && item.Start > 0 {
					r["partial"] = true
				}
				at, _ := time.Parse(time.RFC3339Nano, ts)
				for j := len(recent) - 1; j >= max(0, len(recent)-16); j-- {
					candidate, err := time.Parse(time.RFC3339Nano, recent[j])
					if err == nil && !at.IsZero() && !candidate.After(at) {
						r["startTs"] = recent[j]
						break
					}
				}
				push(r)
				assistants[key] = r
			} else {
				r["loc"] = append(r["loc"].([][2]int64), loc)
				r["endTs"] = ts
				if body != "" {
					if text(r["preview"]) == "" {
						r["preview"] = Preview(stripMarkdown(body), 512)
					}
					r["toolOnly"] = false
				}
				if thinking {
					r["hasThinking"] = true
				}
			}
			for k, v := range map[string]string{"model": text(m["model"]), "requestId": text(e["requestId"]), "stopReason": text(m["stop_reason"])} {
				if v != "" && (k == "stopReason" || text(r[k]) == "") {
					r[k] = v
				}
			}
			if e["isApiErrorMessage"] == true || e["error"] != nil {
				r["isError"] = true
			}
			if merged := mergeUsage(objectUsage(r["usage"]), u); merged != nil {
				r["usage"] = merged
			}
			for _, b := range blocks {
				v := object(b)
				if v["type"] != "tool_use" || text(v["id"]) == "" {
					continue
				}
				tool := text(v["id"])
				name := text(v["name"])
				fields := Row{"toolName": name, "toolUseId": tool, "parentId": "a:" + key, "inputPreview": Preview(string(raw(v["input"])), 300), "preview": name}
				if sum := inputSummary(name, v["input"]); sum != "" {
					fields["inputSummary"] = sum
				}
				tr := tools[tool]
				if tr == nil {
					tr = Row{"id": "t:" + tool, "kind": "tool", "turn": turn, "ts": ts, "loc": [][2]int64{loc}}
					push(tr)
					tools[tool] = tr
				} else {
					tr["loc"] = append([][2]int64{loc}, tr["loc"].([][2]int64)...)
					tr["ts"] = ts
				}
				for k, v := range fields {
					tr[k] = v
				}
			}
		case "attachment":
			v := object(e["attachment"])
			if len(v) == 0 {
				break
			}
			t := first(v["type"], "unknown")
			if t == "queued_command" && v["isMeta"] != true && (v["commandMode"] == nil || v["commandMode"] == "prompt") {
				r := base("u", "user", contentPreview(v["prompt"], 512))
				r["label"] = "queued"
				push(r)
			} else {
				r := base("c", "context", Preview(attachmentPreview(v), 512))
				r["label"], r["attachmentType"] = t, t
				push(r)
			}
		case "system":
			sub := text(e["subtype"])
			if sub == "" || sub == "turn_duration" || sub == "api_metrics" {
				break
			}
			kind, prefix, label := "context", "c", sub
			preview := text(e["content"])
			switch sub {
			case "compact_boundary", "microcompact_boundary":
				kind, prefix = "compact", "m"
				meta := object(e["compactMetadata"])
				if len(meta) == 0 {
					meta = object(e["microcompactMetadata"])
				}
				parts := []string{}
				if s := text(meta["trigger"]); s != "" {
					parts = append(parts, s)
				}
				if v := meta["preTokens"]; v != nil {
					parts = append(parts, fmt.Sprintf("%v tokens", v))
				}
				if len(parts) > 0 {
					preview = strings.Join(parts, " · ")
				}
			case "api_error":
				label = "api_error"
				if attempt, ok := e["retryAttempt"].(float64); ok && (e["maxRetries"] == nil || attempt <= number(e["maxRetries"])) {
					label = "api_retry"
				}
				if preview == "" {
					preview = first(object(e["error"])["message"], object(object(e["error"])["error"])["message"], object(e["cause"])["code"])
				}
			case "stop_hook_summary":
				label = "hook_summary"
				preview = fmt.Sprintf("%v hooks ×%v", e["hookLabel"], e["hookCount"])
			case "streaming_fallback":
				label = "stream_retry"
			case "scheduled_task_fire":
				label = "scheduled_task"
			case "local_command":
				preview = commandText(preview)
			}
			if sub == "api_error" {
				label, preview = apiErrorPreview(e)
			}
			if sub == "stop_hook_summary" {
				preview = hookSummaryPreview(e)
			}
			if preview == "" {
				preview = systemRecordText(e)
			}
			if sub == "model_refusal_fallback" {
				label = "model_fallback"
			}
			r := base(prefix, kind, Preview(preview, 512))
			r["label"] = label
			known := map[string]bool{"local_command": true, "stop_hook_summary": true, "streaming_fallback": true, "informational": true, "away_summary": true, "scheduled_task_fire": true, "permission_retry": true, "memory_saved": true, "agents_killed": true, "model_refusal_fallback": true, "api_error": true, "compact_boundary": true, "microcompact_boundary": true}
			if label == "api_error" || (sub == "informational" || !known[sub]) && e["level"] == "error" || label == "hook_summary" && len(array(e["hookErrors"])) > 0 {
				r["isError"] = true
			}
			push(r)
		}
		remember(ts)
	}
	for _, r := range tools {
		if end := text(r["resultTs"]); end != "" && end != r["ts"] {
			r["endTs"] = end
		}
	}
	return rows
}
func objectUsage(v any) Object {
	if v == nil {
		return nil
	}
	return object(v)
}
func RecordCost(raw json.RawMessage) int64 {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return int64(len(raw))
	}
	nodes := 0
	var walk func(any) int64
	walk = func(v any) int64 {
		nodes++
		if nodes > 20000 {
			return 0
		}
		switch x := v.(type) {
		case string:
			return int64(min(2048, len(utf16.Encode([]rune(x)))) + 2)
		case []any:
			c := int64(2 + len(x))
			for _, v := range x {
				c += walk(v)
			}
			return c
		case map[string]any:
			c := int64(0)
			for k, v := range x {
				c += int64(len(k)+4) + walk(v)
			}
			return c
		default:
			return 8
		}
	}
	return walk(v)
}
func projectionEntries(page sessionstore.Page) ([]Entry, int) {
	out := []Entry{}
	for _, e := range page.Entries {
		if v := parse(e.Entry); v != nil {
			out = append(out, Entry{v, e.ByteStart, e.ByteEnd, 0})
		}
	}
	represented := 0
	for _, e := range page.Oversized {
		v := Oversized(e.Head)
		if v["type"] == "user" && len(array(v["toolUseIds"])) > 0 {
			out = append(out, Entry{v, e.ByteStart, e.ByteEnd, e.ByteEnd - e.ByteStart})
			represented++
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Start < out[b].Start })
	return out, represented
}
