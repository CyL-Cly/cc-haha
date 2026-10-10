package server

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

// This is the Source subagentRunService projection. Its inputs remain physical
// transcript evidence; metadata can identify a file, but never supplies a task.
type subagentResolution struct {
	agentID, description, prompt, result, updatedAt string
	usage                                           map[string]float64
	hasResult, async, isError                       bool
}
type nestedAgentRef struct{ parent, leaf string }
type subagentTranscript struct {
	agentID         string
	messages        []json.RawMessage
	notifications   []historyObject
	historyComplete bool
}
type subagentFragment struct {
	subagentTranscript
	modifiedAt float64
}

func emptySubagentTranscript() subagentTranscript {
	return subagentTranscript{messages: []json.RawMessage{}, notifications: []historyObject{}, historyComplete: true}
}

var strictAgentID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const subagentJSSpace = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var agentIDText = regexp.MustCompile(`(?:^|\n)` + subagentJSSpace + `*(?:agentId|agent_id):` + subagentJSSpace + `*([A-Za-z0-9_@.-]+)`)
var agentIDLine = regexp.MustCompile(`^` + subagentJSSpace + `*(?:agentId|agent_id):` + subagentJSSpace + `*[A-Za-z0-9_@.-]+`)
var agentUsageText = regexp.MustCompile(`(?is)<usage>(.*?)</usage>`)

func agentIDHint(value string) string {
	if value == "" || len(utf16.Encode([]rune(value))) > 256 || !strictAgentID.MatchString(value) {
		return ""
	}
	return value
}
func canonicalNestedAgentRef(value string) *nestedAgentRef {
	last := strings.LastIndex(value, "/")
	if last <= 0 || last == len(value)-1 {
		return nil
	}
	before, leaf := value[:last], value[last+1:]
	parentStart := strings.LastIndex(before, "/")
	if parentStart <= 0 || parentStart == len(before)-1 {
		return nil
	}
	parent := before[parentStart+1:]
	if len(utf16.Encode([]rune(parent))) > 256 || strings.ContainsRune(parent, 0) || len(utf16.Encode([]rune(leaf))) > 256 {
		return nil
	}
	return &nestedAgentRef{parent, leaf}
}
func subagentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	values := []string{}
	for _, part := range parts {
		text := ""
		if json.Unmarshal(part, &text) != nil {
			block := historyRecord(part)
			if raw := block["text"]; len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
				text = subagentText(block["content"])
			}
		}
		if text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}
func subagentTimestamp(value string) (float64, bool) {
	millis, valid, err := sessionprojection.ParseDateUTC(utf16.Encode([]rune(value)))
	return millis, valid && err == nil
}
func latestSubagentTimestamp(values ...string) string {
	latest, latestMS := "", math.Inf(-1)
	for _, value := range values {
		if millis, valid := subagentTimestamp(value); valid && millis >= latestMS {
			latest, latestMS = value, millis
		}
	}
	return latest
}
func normalizeSubagentUsage(usage map[string]float64) map[string]float64 {
	result := map[string]float64{}
	for _, key := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if value, exists := usage[key]; exists && !math.IsNaN(value) && !math.IsInf(value, 0) {
			result[key] = value
		}
	}
	if _, exists := result["totalTokens"]; !exists {
		input, hasInput := result["inputTokens"]
		output, hasOutput := result["outputTokens"]
		if hasInput && hasOutput {
			result["totalTokens"] = input + output
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func subagentUsageJSON(usage map[string]float64) historyObject {
	result := historyObject{}
	for key, value := range usage {
		// ECMAScript stringify emits null for an inferred overflowing total.
		if math.IsNaN(value) || math.IsInf(value, 0) {
			result[key] = json.RawMessage("null")
		} else {
			result[key] = json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64))
		}
	}
	return result
}
func mergeSubagentUsage(preferred, fallback map[string]float64) map[string]float64 {
	if preferred == nil {
		return fallback
	}
	if fallback == nil {
		return preferred
	}
	result := map[string]float64{}
	for _, key := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if value, exists := preferred[key]; exists {
			result[key] = value
		} else if value, exists := fallback[key]; exists {
			result[key] = value
		}
	}
	return normalizeSubagentUsage(result)
}
func extractSubagentUsage(text string) map[string]float64 {
	if match := agentUsageText.FindStringSubmatch(text); match != nil {
		text = match[1]
	}
	usage := map[string]float64{}
	for key, names := range map[string][]string{"inputTokens": {"input_tokens", "inputTokens"}, "outputTokens": {"output_tokens", "outputTokens"}, "totalTokens": {"total_tokens", "totalTokens"}} {
		for _, name := range names {
			match := regexp.MustCompile(`(?i)<` + name + `>` + subagentJSSpace + `*(\d+)` + subagentJSSpace + `*</` + name + `>`).FindStringSubmatch(text)
			if match == nil {
				match = regexp.MustCompile(`(?i)(?:^|\n)` + subagentJSSpace + `*` + name + subagentJSSpace + `*[:=]` + subagentJSSpace + `*(\d+)`).FindStringSubmatch(text)
			}
			if match != nil {
				value, _ := strconv.ParseFloat(match[1], 64)
				usage[key] = value
				break
			}
		}
	}
	return normalizeSubagentUsage(usage)
}
func subagentTranscriptUsage(messages []json.RawMessage) map[string]float64 {
	usage := map[string]float64{}
	for _, raw := range messages {
		entry := historyRecord(raw)
		actual := historyRecord(entry["usage"])
		for key, field := range map[string]string{"inputTokens": "input_tokens", "outputTokens": "output_tokens"} {
			var value float64
			if raw := actual[field]; len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &value) == nil {
				usage[key] += value
			}
		}
	}
	return normalizeSubagentUsage(usage)
}
func resolveSubagentMessages(messages []json.RawMessage, toolRef string) *subagentResolution {
	resolution := &subagentResolution{}
	found := false
	for _, raw := range messages {
		entry := historyRecord(raw)
		content := entry["content"]
		if len(content) == 0 || string(content) == "null" {
			content = historyRecord(entry["message"])["content"]
		}
		for _, block := range contentBlocks(content) {
			if stringField(block, "type") == "tool_use" && stringField(block, "name") == "Agent" && stringField(block, "id") == toolRef {
				found = true
				resolution.updatedAt = latestSubagentTimestamp(resolution.updatedAt, stringField(entry, "timestamp"))
				input := historyRecord(block["input"])
				if value := stringField(input, "description"); value != "" {
					resolution.description = value
				}
				if value := stringField(input, "prompt"); value != "" {
					resolution.prompt = value
				}
			}
			if stringField(block, "type") == "tool_result" && stringField(block, "tool_use_id") == toolRef {
				resolution.hasResult = true
				resolution.isError = resolution.isError || string(block["is_error"]) == "true"
				resolution.updatedAt = latestSubagentTimestamp(resolution.updatedAt, stringField(entry, "timestamp"))
				text := subagentText(block["content"])
				if match := agentIDText.FindStringSubmatch(text); match != nil {
					resolution.agentID = match[1]
				}
				resolution.async = resolution.async || (strings.Contains(text, "Async agent launched successfully.") && strings.Contains(text, "The agent is working in the background."))
				lines := []string{}
				for _, line := range strings.Split(agentUsageText.ReplaceAllString(text, ""), "\n") {
					if !agentIDLine.MatchString(line) {
						lines = append(lines, line)
					}
				}
				if value := trimJS(strings.Join(lines, "\n")); value != "" {
					resolution.result = value
				}
				resolution.usage = mergeSubagentUsage(extractSubagentUsage(text), resolution.usage)
			}
		}
	}
	if !found {
		return nil
	}
	return resolution
}
func notificationForAgent(notifications []historyObject, toolRef, owner string) historyObject {
	candidates, exact := []historyObject{}, []historyObject{}
	for _, notice := range notifications {
		if stringField(notice, "toolUseId") != toolRef {
			continue
		}
		candidates = append(candidates, notice)
		if stringField(notice, "ownerAgentId") == owner {
			exact = append(exact, notice)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) > 1 {
		return nil
	}
	if owner != "" && len(candidates) == 1 && stringField(candidates[0], "ownerAgentId") == "" {
		return candidates[0]
	}
	return nil
}
func truncateSubagentMessages(messages []json.RawMessage) ([]json.RawMessage, bool) {
	if len(messages) <= 1000 {
		return messages, false
	}
	return append(append([]json.RawMessage{}, messages[:50]...), messages[len(messages)-950:]...), true
}
func projectSubagentActivity(transcript subagentTranscript, all bool) subagentTranscript {
	scoped := map[string]string{}
	for _, raw := range transcript.messages {
		for _, block := range contentBlocks(historyRecord(raw)["content"]) {
			if stringField(block, "type") == "tool_use" && (stringField(block, "name") == "Agent" || all) {
				if id := stringField(block, "id"); id != "" {
					scoped[id] = transcript.agentID + "/" + id
				}
			}
		}
	}
	if len(scoped) == 0 {
		return transcript
	}
	result := transcript
	result.messages = []json.RawMessage{}
	result.notifications = []historyObject{}
	for _, raw := range transcript.messages {
		entry := historyRecord(raw)
		var blocks []json.RawMessage
		if json.Unmarshal(entry["content"], &blocks) != nil {
			result.messages = append(result.messages, raw)
			continue
		}
		changed := false
		for i, raw := range blocks {
			block := historyRecord(raw)
			field := ""
			if stringField(block, "type") == "tool_use" {
				field = "id"
			} else if stringField(block, "type") == "tool_result" {
				field = "tool_use_id"
			}
			if id := scoped[stringField(block, field)]; field != "" && id != "" {
				block[field] = jsonString(id)
				blocks[i], _ = json.Marshal(block)
				changed = true
			}
		}
		if changed {
			entry["content"], _ = json.Marshal(blocks)
			raw, _ = json.Marshal(entry)
		}
		result.messages = append(result.messages, raw)
	}
	for _, notice := range transcript.notifications {
		if id := scoped[stringField(notice, "toolUseId")]; id != "" {
			cloned := historyObject{}
			for key, value := range notice {
				cloned[key] = value
			}
			cloned["toolUseId"] = jsonString(id)
			notice = cloned
		}
		result.notifications = append(result.notifications, notice)
	}
	return result
}
func mergeSubagentFragments(fragments []subagentFragment) subagentTranscript {
	result := emptySubagentTranscript()
	seen := map[string]bool{}
	positions := map[string]int{}
	for _, fragment := range fragments {
		result.agentID = fragment.agentID
		result.historyComplete = result.historyComplete && fragment.historyComplete
		for _, raw := range fragment.messages {
			id := stringField(historyRecord(raw), "id")
			if !seen[id] {
				seen[id] = true
				result.messages = append(result.messages, raw)
			}
		}
		for _, notice := range fragment.notifications {
			id := stringField(notice, "toolUseId")
			if position, exists := positions[id]; exists {
				result.notifications[position] = notice
			} else {
				positions[id] = len(result.notifications)
				result.notifications = append(result.notifications, notice)
			}
		}
	}
	sort.SliceStable(result.messages, func(i, j int) bool {
		left, validLeft := subagentTimestamp(stringField(historyRecord(result.messages[i]), "timestamp"))
		if !validLeft {
			left = 0
		}
		right, validRight := subagentTimestamp(stringField(historyRecord(result.messages[j]), "timestamp"))
		if !validRight {
			right = 0
		}
		return left < right
	})
	return result
}
func subagentActivityFragments(fragments []subagentFragment) subagentTranscript {
	identities := make([][]string, len(fragments))
	for i, fragment := range fragments {
		for _, raw := range fragment.messages {
			value, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
			identities[i] = append(identities[i], string(value))
		}
	}
	kept := []subagentFragment{}
	for i, fragment := range fragments {
		superseded := false
		for j, candidate := range identities {
			if i == j || len(identities[i]) >= len(candidate) {
				continue
			}
			prefix := true
			for index, value := range identities[i] {
				if value != candidate[index] {
					prefix = false
					break
				}
			}
			if prefix {
				superseded = true
				break
			}
		}
		if !superseded {
			projected := projectSubagentActivity(fragment.subagentTranscript, true)
			kept = append(kept, subagentFragment{projected, fragment.modifiedAt})
		}
	}
	return mergeSubagentFragments(kept)
}
