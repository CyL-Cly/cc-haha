package teamreader

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func stringRaw(value json.RawMessage) bool {
	_, err := protocol.DecodeJavaScriptJSONString(value)
	return err == nil
}
func nonemptyRaw(value json.RawMessage) bool {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	return err == nil && len(units) > 0
}
func utf16Less(a, b json.RawMessage) bool {
	left, _ := protocol.DecodeJavaScriptJSONString(a)
	right, _ := protocol.DecodeJavaScriptJSONString(b)
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

type mailboxGroup struct {
	from, text, timestamp, summary, color json.RawMessage
	recipients                            map[string]bool
}

// ReadWorkbenchMessages leaves every unread marker untouched. New stable IDs
// and the legacy per-recipient occurrence counter are independent identities.
func (s *Service) ReadWorkbenchMessages(team string) ([]protocol.JSONObject, error) {
	directory := filepath.Join(s.teamDirectory(team), "inboxes")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []protocol.JSONObject{}, nil
	}
	files := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Slice(files, func(i, j int) bool { return utf16Less(raw(files[i]), raw(files[j])) })
	groups := map[string]*mailboxGroup{}
	ids := []string{}
	occurrences := map[string]int{}
	for _, file := range files {
		recipient := strings.TrimSuffix(file, ".json")
		data, err := os.ReadFile(filepath.Join(directory, file))
		if err != nil {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(protocol.DecodeJavaScriptUTF8(data), &items) != nil {
			continue
		}
		for _, item := range items {
			value := obj(item)
			from, text, timestamp := value.Get("from"), value.Get("text"), value.Get("timestamp")
			if !stringRaw(from) || !stringRaw(text) || !stringRaw(timestamp) {
				continue
			}
			summary, color := value.Get("summary"), value.Get("color")
			if !stringRaw(summary) {
				summary = raw("")
			}
			if !stringRaw(color) {
				color = raw("")
			}
			units, _ := protocol.DecodeJavaScriptJSONString(timestamp)
			if len(units) > 19 {
				units = units[:19]
			}
			key := hash(protocol.StringifyValidatedJavaScriptJSON(raw([]json.RawMessage{from, text, protocol.QuoteJavaScriptUTF16(units), summary, color})))
			occurrenceKey := recipient + "\x00" + key
			occurrence := occurrences[occurrenceKey]
			occurrences[occurrenceKey]++
			id := raw("legacy:" + key + ":" + strconv.Itoa(occurrence))
			if valueID := value.Get("id"); nonemptyRaw(valueID) {
				idUnits, _ := protocol.DecodeJavaScriptJSONString(valueID)
				prefix, _ := protocol.DecodeJavaScriptJSONString(raw("mailbox:"))
				id = protocol.QuoteJavaScriptUTF16(append(prefix, idUnits...))
			}
			identity := string(id)
			existing := groups[identity]
			if existing != nil {
				existing.recipients[recipient] = true
				if utf16Less(timestamp, existing.timestamp) {
					existing.timestamp = timestamp
				}
			} else {
				groups[identity] = &mailboxGroup{from: from, text: text, timestamp: timestamp, summary: summary, color: color, recipients: map[string]bool{recipient: true}}
				ids = append(ids, identity)
			}
		}
	}
	result := []protocol.JSONObject{}
	for _, id := range ids {
		message := groups[id]
		recipients := []string{}
		for recipient := range message.recipients {
			recipients = append(recipients, recipient)
		}
		sort.Slice(recipients, func(i, j int) bool { return utf16Less(raw(recipients[i]), raw(recipients[j])) })
		structured := obj([]byte(obj(raw(map[string]json.RawMessage{"s": message.text})).String("s")))
		protocolType, taskID := structured.Get("type"), structured.Get("taskId")
		if !stringRaw(protocolType) {
			protocolType = nil
		}
		if !stringRaw(taskID) {
			taskID = nil
		}
		text := message.text
		if value := structured.Get("subject"); stringRaw(value) {
			text = value
		} else if value := structured.Get("reason"); stringRaw(value) {
			text = value
		}
		kind, to := "direct", "*"
		if len(recipients) > 0 {
			to = recipients[0]
		}
		if nonemptyRaw(protocolType) {
			kind = "system"
		} else if len(recipients) > 1 {
			kind, to = "broadcast", "*"
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: json.RawMessage(id)}, protocol.JSONField{Name: "from", Value: message.from}, field("to", to), field("recipients", recipients), field("kind", kind), protocol.JSONField{Name: "text", Value: text}, protocol.JSONField{Name: "timestamp", Value: message.timestamp})
		for _, p := range []protocol.JSONField{{Name: "summary", Value: message.summary}, {Name: "color", Value: message.color}, {Name: "taskId", Value: taskID}, {Name: "protocolType", Value: protocolType}} {
			if nonemptyRaw(p.Value) {
				row.Set(p.Name, p.Value)
			}
		}
		result = append(result, row)
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if cmp := localeCompare(a.String("timestamp"), b.String("timestamp")); cmp != 0 {
			return cmp < 0
		}
		return localeCompare(a.String("id"), b.String("id")) < 0
	})
	return result, nil
}
