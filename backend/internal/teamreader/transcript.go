package teamreader

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"regexp"
	"strings"
)

type TranscriptProjection struct {
	OwnerAgentID   string
	OwnerAgentIDs  []string
	OwnerByOrdinal []string
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func fragmentID(owner string, id json.RawMessage) json.RawMessage {
	prefix, _ := protocol.DecodeJavaScriptJSONString(raw(owner + "/"))
	units, _ := protocol.DecodeJavaScriptJSONString(id)
	if len(units) >= len(prefix) {
		same := true
		for i := range prefix {
			same = same && units[i] == prefix[i]
		}
		if same {
			return id
		}
	}
	return protocol.QuoteJavaScriptUTF16(append(prefix, units...))
}
func projectContent(value json.RawMessage, owner string) json.RawMessage {
	if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
		var values []json.RawMessage
		_ = json.Unmarshal(value, &values)
		for i, item := range values {
			values[i] = projectContent(item, owner)
		}
		return raw(values)
	}
	v := obj(value)
	if !v.IsObject() {
		return value
	}
	if v.String("type") == "tool_use" && stringRaw(v.Get("id")) {
		id := v.Get("id")
		v.Set("id", fragmentID(owner, id))
		original := v.Get("original_tool_use_id")
		if !nonblank(original) {
			original = id
		}
		v.Set("original_tool_use_id", original)
		return raw(v)
	}
	if v.String("type") == "tool_result" && stringRaw(v.Get("tool_use_id")) {
		id := v.Get("tool_use_id")
		v.Set("tool_use_id", fragmentID(owner, id))
		original := v.Get("original_tool_use_id")
		if !nonblank(original) {
			original = id
		}
		v.Set("original_tool_use_id", original)
		if child := v.Get("content"); child != nil {
			v.Set("content", projectContent(child, owner))
		}
		return raw(v)
	}
	if child := v.Get("content"); child != nil {
		v.Set("content", projectContent(child, owner))
		return raw(v)
	}
	return value
}
func projectUseResult(value json.RawMessage, owner string) json.RawMessage {
	v := obj(value)
	if !v.IsObject() {
		return value
	}
	for _, key := range []string{"backgroundTaskId", "background_task_id"} {
		if id := v.Get(key); nonblank(id) {
			v.Set(key, fragmentID(owner, id))
		}
	}
	return raw(v)
}
func (s *Service) transcriptMessage(entry protocol.JSONObject, owner string) *protocol.JSONObject {
	typ := entry.String("type")
	if typ != "user" && typ != "assistant" && typ != "system" && typ != "tool_use" && typ != "tool_result" || jsTruthy(entry.Get("isMeta")) {
		return nil
	}
	message := obj(entry.Get("message"))
	content := message.Get("content")
	if content == nil {
		content = entry.Get("content")
		if content == nil || string(content) == "null" {
			content = entry.Get("message")
		}
		if content == nil || string(content) == "null" {
			content = json.RawMessage("null")
		}
	}
	id := entry.Get("uuid")
	if !jsTruthy(id) {
		id = raw(randomID())
	}
	timestamp := entry.Get("timestamp")
	if !jsTruthy(timestamp) {
		timestamp = raw(s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	model := entry.Get("model")
	if !stringRaw(model) {
		model = message.Get("model")
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: id}, field("type", typ), protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "timestamp", Value: timestamp})
	if parent := entry.Get("parentToolUseId"); stringRaw(parent) {
		result.Set("parentToolUseId", parent)
	}
	if nonemptyRaw(model) {
		result.Set("model", model)
	}
	result.Set("toolUseResult", entry.Get("toolUseResult"))
	if owner != "" {
		if !stringRaw(id) {
			return nil
		}
		result.Set("id", fragmentID(owner, id))
		result.Set("content", projectContent(content, owner))
		if id := result.Get("parentToolUseId"); nonemptyRaw(id) {
			result.Set("parentToolUseId", fragmentID(owner, id))
		}
		if v := result.Get("toolUseResult"); v != nil {
			result.Set("toolUseResult", projectUseResult(v, owner))
		}
	}
	return &result
}
func taskAnchors(message protocol.JSONObject) []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, block := range rows(message.Get("content")) {
		if block.String("type") != "tool_use" || !nonblank(block.Get("name")) || block.String("name") != "TaskUpdate" {
			continue
		}
		input := obj(block.Get("input"))
		status := input.String("status")
		if !nonblank(input.Get("taskId")) || status != "pending" && status != "in_progress" && status != "completed" {
			continue
		}
		result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: input.Get("taskId")}, field("status", status), protocol.JSONField{Name: "messageId", Value: message.Get("id")}, protocol.JSONField{Name: "timestamp", Value: message.Get("timestamp")}))
	}
	return result
}

var notificationBlock = regexp.MustCompile(`(?is)<task-notification>\s*.*?</task-notification>`)

func xmlText(text string) string {
	for _, pair := range [][2]string{{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", `"`}, {"&#39;", "'"}, {"&amp;", "&"}} {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}
	return text
}
func xmlTag(text, tag string) json.RawMessage {
	matched := regexp.MustCompile(`(?is)<` + tag + `>(.*?)</` + tag + `>`).FindStringSubmatch(text)
	if len(matched) < 2 || matched[1] == "" {
		return nil
	}
	return raw(xmlText(protocol.TrimJavaScriptString(matched[1])))
}
func transcriptText(value json.RawMessage) string {
	if stringRaw(value) {
		return obj(raw(map[string]json.RawMessage{"v": value})).String("v")
	}
	var blocks []json.RawMessage
	if json.Unmarshal(value, &blocks) != nil {
		return ""
	}
	parts := []string{}
	for _, block := range blocks {
		var text string
		if stringRaw(block) {
			text = obj(raw(map[string]json.RawMessage{"v": block})).String("v")
		} else {
			text = obj(block).String("text")
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
func taskNotification(entry protocol.JSONObject, owner string) *protocol.JSONObject {
	timestamp := entry.Get("timestamp")
	if !nonblank(timestamp) {
		timestamp = nil
	}
	var source protocol.JSONObject
	if entry.String("type") == "cc-haha-task-notification" {
		source = obj(entry.Get("taskNotification"))
		if !source.IsObject() {
			return nil
		}
		if value := source.Get("timestamp"); nonblank(value) {
			timestamp = value
		}
	} else {
		message := obj(entry.Get("message"))
		if entry.String("type") != "user" || message.String("role") != "user" {
			return nil
		}
		xml := notificationBlock.FindString(transcriptText(message.Get("content")))
		if xml == "" {
			return nil
		}
		source = protocol.NewJSONObject()
		for _, pair := range [][2]string{{"toolUseId", "tool-use-id"}, {"taskId", "task-id"}, {"status", "status"}, {"summary", "summary"}, {"result", "result"}, {"outputFile", "output-file"}} {
			source.Set(pair[0], xmlTag(xml, pair[1]))
		}
	}
	tool := source.Get("toolUseId")
	status := source.String("status")
	if status == "killed" {
		status = "stopped"
	}
	if !nonblank(tool) || status != "completed" && status != "failed" && status != "stopped" {
		return nil
	}
	id := source.Get("taskId")
	if !nonblank(id) {
		id = tool
	}
	if owner != "" {
		id, tool = fragmentID(owner, id), fragmentID(owner, tool)
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: id}, protocol.JSONField{Name: "toolUseId", Value: tool}, field("status", status))
	for _, key := range []string{"summary", "result", "outputFile"} {
		if value := source.Get(key); nonblank(value) {
			result.Set(key, value)
		}
	}
	result.Set("timestamp", timestamp)
	return &result
}
func cursorForBuffer(data []byte, identity *string, ctime float64, ordinal int) protocol.JSONObject {
	firstEnd := min(len(data), 64*1024)
	lastStart := max(0, len(data)-64*1024)
	return protocol.NewJSONObject(field("version", 2), field("size", len(data)), field("ctimeMs", ctime), field("fileIdentity", identity), field("firstWindowHash", hash(data[:firstEnd])), field("lastWindowHash", hash(data[lastStart:])), field("afterOrdinal", ordinal))
}
func encodeCursor(value protocol.JSONObject) string {
	return base64.RawURLEncoding.EncodeToString(protocol.StringifyValidatedJavaScriptJSON(raw(value)))
}
func decodeCursor(value string) *protocol.JSONObject {
	if value == "" {
		return nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(value, "+", "-"), "/", "_"), "="))
	if err != nil {
		return nil
	}
	v := obj(protocol.DecodeJavaScriptUTF8(decoded))
	identity := v.Get("fileIdentity")
	if string(v.Get("version")) != "2" || !safeInteger(v.Get("size")) || numeric(v.Get("size")) < 0 || math.IsNaN(numeric(v.Get("ctimeMs"))) || math.IsInf(numeric(v.Get("ctimeMs")), 0) || numeric(v.Get("ctimeMs")) < 0 || !safeInteger(v.Get("afterOrdinal")) || numeric(v.Get("afterOrdinal")) < -1 || string(identity) != "null" && !stringRaw(identity) || !stringRaw(v.Get("firstWindowHash")) || !stringRaw(v.Get("lastWindowHash")) {
		return nil
	}
	return &v
}
func preservesPrefix(data []byte, cursor protocol.JSONObject, identity *string) bool {
	size := int(numeric(cursor.Get("size")))
	if len(data) <= size {
		return false
	}
	if old := cursor.Get("fileIdentity"); string(old) != "null" && identity != nil && cursor.String("fileIdentity") != *identity {
		return false
	}
	return hash(data[:min(size, 64*1024)]) == cursor.String("firstWindowHash") && hash(data[max(0, size-64*1024):size]) == cursor.String("lastWindowHash")
}
func (s *Service) ParseTranscriptBuffer(data []byte, options protocol.JSONObject, ctime float64, identity *string, projection TranscriptProjection) protocol.JSONObject {
	type parsed struct {
		ordinal               int
		message, notification *protocol.JSONObject
		anchors               []protocol.JSONObject
	}
	entries := []parsed{}
	ordinal := -1
	for _, line := range strings.Split(string(protocol.DecodeJavaScriptUTF8(data)), "\n") {
		if protocol.TrimJavaScriptString(line) == "" {
			continue
		}
		encoded, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(line))
		if err != nil {
			continue
		}
		ordinal++
		entry := obj(encoded)
		owner := projection.OwnerAgentID
		if ordinal < len(projection.OwnerByOrdinal) {
			owner = projection.OwnerByOrdinal[ordinal]
		}
		message := s.transcriptMessage(entry, owner)
		notification := taskNotification(entry, owner)
		anchors := []protocol.JSONObject{}
		if message != nil {
			anchors = taskAnchors(*message)
		}
		if message != nil || notification != nil {
			entries = append(entries, parsed{ordinal: ordinal, message: message, notification: notification, anchors: anchors})
		}
	}
	signature := hash(data)
	previous := decodeCursor(options.String("cursor"))
	after := -1
	if options.Get("afterOrdinal") != nil {
		after = int(numeric(options.Get("afterOrdinal")))
	}
	reset := false
	if options.Get("cursor") != nil && previous == nil || after >= 0 && options.String("signature") == "" || after > ordinal || previous != nil && int(numeric(previous.Get("afterOrdinal"))) != after {
		after, reset = -1, true
	} else if options.String("signature") != "" {
		if options.String("signature") == signature {
			if previous != nil && (numeric(previous.Get("size")) != float64(len(data)) || numeric(previous.Get("ctimeMs")) != ctime || string(previous.Get("fileIdentity")) != "null" && identity != nil && previous.String("fileIdentity") != *identity) {
				after, reset = -1, true
			}
		} else if previous == nil || !preservesPrefix(data, *previous, identity) {
			after, reset = -1, true
		}
	}
	messages, anchors := []protocol.JSONObject{}, []protocol.JSONObject{}
	notifications := []protocol.JSONObject{}
	indices := map[string]int{}
	for _, entry := range entries {
		if entry.ordinal <= after {
			continue
		}
		if entry.message != nil {
			messages = append(messages, *entry.message)
		}
		anchors = append(anchors, entry.anchors...)
		if entry.notification != nil {
			key := string(entry.notification.Get("toolUseId"))
			if index, exists := indices[key]; exists {
				notifications[index] = *entry.notification
			} else {
				indices[key] = len(notifications)
				notifications = append(notifications, *entry.notification)
			}
		}
	}
	owners := projection.OwnerAgentIDs
	if owners == nil {
		owners = []string{}
	}
	result := protocol.NewJSONObject(field("messages", messages), field("ownerAgentIds", owners), field("taskAnchors", anchors), field("taskNotifications", notifications), field("signature", signature), field("cursor", encodeCursor(cursorForBuffer(data, identity, ctime, ordinal))), field("afterOrdinal", ordinal))
	if reset {
		result.Set("reset", raw(true))
	}
	return result
}
