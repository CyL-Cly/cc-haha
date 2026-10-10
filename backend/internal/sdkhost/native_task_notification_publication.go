package sdkhost

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func notificationTag(text []uint16, tag string) ([]uint16, bool) {
	find := func(from int, token string) int {
		for i := from; i+len(token) <= len(text); i++ {
			match := true
			for j := range token {
				if text[i+j] != uint16(token[j]) {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}
	open := "<" + tag + ">"
	start := find(0, open)
	if start < 0 {
		return nil, false
	}
	start += len(open)
	end := find(start, "</"+tag+">")
	if end < 0 {
		return nil, false
	}
	return text[start:end], true
}
func notificationParseInt(text []uint16) json.RawMessage {
	start := 0
	for start < len(text) && protocol.IsJavaScriptWhitespace(rune(text[start])) {
		start++
	}
	end := start
	if end < len(text) && (text[end] == '+' || text[end] == '-') {
		end++
	}
	digits := end
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == digits {
		return []byte("null")
	}
	ascii := make([]byte, end-start)
	for i := range ascii {
		ascii[i] = byte(text[start+i])
	}
	number, _ := strconv.ParseFloat(string(ascii), 64)
	if math.IsInf(number, 0) || math.IsNaN(number) {
		return []byte("null")
	}
	if number == 0 {
		return []byte("0")
	}
	raw, _ := json.Marshal(number)
	return raw
}

// This is print.ts's shared queue observer. Only admitted root notification
// commands become SDK bookends; their normal model queue lifecycle is retained.
func (h *Host) publishTaskNotificationOnOwner(scope *cooperative.Scope, command sessioncontroller.TaskNotificationCommand) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	if h.closing || h.isAgent() || command.Mode != "task-notification" || command.AgentID != "" {
		return false, nil
	}
	text, err := protocol.DecodeJavaScriptJSONString(command.Content)
	if err != nil {
		text = nil
	}
	statusUnits, _ := notificationTag(text, "status")
	status := ""
	for _, unit := range statusUnits {
		status += string(rune(unit))
	}
	if status == "killed" {
		status = "stopped"
	}
	if status != "completed" && status != "failed" && status != "stopped" {
		return false, nil
	}
	event := protocol.NewJSONObject(notificationField("type", "system"), notificationField("subtype", "task_notification"))
	for _, pair := range [][3]string{{"task_id", "task-id", "required"}, {"tool_use_id", "tool-use-id", "optional"}, {"output_file", "output-file", "required"}, {"summary", "summary", "required"}, {"result", "result", "optional"}, {"workflow_run_id", "workflow-run-id", "optional"}} {
		if pair[0] == "output_file" {
			event.Set("status", quote(status))
		}
		value, present := notificationTag(text, pair[1])
		if present || pair[2] == "required" {
			event.Set(pair[0], protocol.QuoteJavaScriptUTF16(value))
		}
	}
	usageText, _ := notificationTag(text, "usage")
	tokens, _ := notificationTag(usageText, "total_tokens")
	tools, _ := notificationTag(usageText, "tool_uses")
	if len(tokens) > 0 && len(tools) > 0 {
		duration, _ := notificationTag(usageText, "duration_ms")
		millis := []byte("0")
		if len(duration) > 0 {
			millis = notificationParseInt(duration)
		}
		usage := protocol.NewJSONObject(protocol.JSONField{Name: "total_tokens", Value: notificationParseInt(tokens)}, protocol.JSONField{Name: "tool_uses", Value: notificationParseInt(tools)}, protocol.JSONField{Name: "duration_ms", Value: millis})
		data, _ := usage.MarshalJSON()
		event.Set("usage", data)
	}
	event.Set("session_id", quote(h.options.Controller.SessionID()))
	event.Set("uuid", quote(h.options.UUID()))
	// Start/progress producers already enqueue on this same FIFO before queue
	// publication. The observer has no separate ASK receipt or model iterator.
	data, err := protocol.EncodeNDJSON(event)
	if err != nil {
		return true, err
	}
	return true, h.options.Output.Enqueue(data)
}

func notificationField(name, value string) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: quote(value)}
}
