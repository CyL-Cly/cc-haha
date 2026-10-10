package sessioncontroller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// Record follows recordTranscript's prefix-only parent selection and shares
// external transcript cleaning with the independent Record promise. Desktop
// explicit context participates in the chain; progress never participates.
func (writer *Writer) Record(messages []*protocol.JSONObject, promptID json.RawMessage) (json.RawMessage, error) {
	if writer.options.Bootstrap != nil {
		return nil, cooperative.ErrRestricted
	}
	values := make([]LiveRecord, 0, len(messages))
	for _, message := range messages {
		values = append(values, LiveRecord{Record: message})
	}
	cleaned, err := cleanRecordForEnvironment(values, nil, writer.options.Environment)
	if err != nil {
		return nil, err
	}
	records := make([]*protocol.JSONObject, 0, len(cleaned))
	for _, value := range cleaned {
		records = append(records, value.record)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.record(records, promptID, nil)
}
func (writer *Writer) record(messages []*protocol.JSONObject, promptID json.RawMessage, live map[*protocol.JSONObject]*streamhistory.Envelope) (json.RawMessage, error) {
	if writer.async != nil && writer.async.closing {
		return nil, ErrPendingClosed
	}
	retained := []*protocol.JSONObject{}
	for _, message := range messages {
		keep, err := isLoggableRecord(*message, writer.options.Environment)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		if _, err := stringKey(message.Get("uuid")); err != nil {
			return nil, err
		}
		retained = append(retained, message)
	}
	if len(retained) == 0 {
		return json.RawMessage("null"), nil
	}
	if writer.skipped() {
		for _, message := range retained {
			key, _ := stringKey(message.Get("uuid"))
			writer.excluded[key] = true
		}
		writer.pending = nil
		writer.lastPrompt = nil
		return json.RawMessage("null"), nil
	}
	if len(writer.members) > 0 || len(writer.queues[writer.target()]) > 0 {
		info, err := writer.options.Files.Stat(writer.target())
		replaced := errors.Is(err, fs.ErrNotExist)
		if err == nil {
			id, idErr := fileIdentity(info)
			if idErr != nil {
				return nil, idErr
			}
			previous, exists := writer.identities[writer.target()]
			replaced = exists && previous != id
		}
		if replaced {
			for key := range writer.members {
				writer.excluded[key] = true
			}
			clear(writer.members)
			writer.discard(writer.queues[writer.target()])
			writer.queues[writer.target()] = nil
			writer.pending = nil
			writer.lastPrompt = nil
		}
	}
	parent := json.RawMessage("null")
	seenNew := false
	newMessages := []*protocol.JSONObject{}
	for _, message := range retained {
		uuid := message.Get("uuid")
		key, _ := stringKey(uuid)
		if writer.excluded[key] {
			continue
		}
		if writer.members[key] {
			if !seenNew {
				parent = uuid
			}
		} else {
			seenNew = true
			newMessages = append(newMessages, message)
		}
	}
	if len(newMessages) == 0 {
		return parent, nil
	}
	realMessage := false
	for _, message := range newMessages {
		kind := text(message.Get("type"))
		if kind == "user" || kind == "assistant" {
			realMessage = true
		}
	}
	if writer.path == "" && realMessage {
		writer.path = writer.target()
		if err := writer.prepare(writer.path); err != nil {
			return nil, err
		}
		if err := writer.refreshMetadata(true); err != nil {
			return nil, err
		}
		pending := writer.pending
		writer.pending = nil
		for _, entry := range pending {
			if err := writer.append(entry); err != nil {
				return nil, err
			}
		}
	}
	if realMessage {
		if err := writer.prepare(writer.path); err != nil {
			return nil, err
		}
	}
	branch, branchErr := writer.options.Branch()
	for _, message := range newMessages {
		key, _ := stringKey(message.Get("uuid"))
		if writer.excluded[key] {
			continue
		}
		effectiveParent := parent
		if text(message.Get("type")) == "user" && truthy(message.Get("sourceToolAssistantUUID")) {
			id := message.Get("sourceToolAssistantUUID")
			idKey, err := stringKey(id)
			if err != nil {
				return nil, err
			}
			if !writer.excluded[idKey] {
				effectiveParent = id
			}
		}
		compact := text(message.Get("type")) == "system" && text(message.Get("subtype")) == "compact_boundary"
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "parentUuid", Value: effectiveParent}, protocol.JSONField{Name: "logicalParentUuid"}, protocol.JSONField{Name: "isSidechain", Value: raw(false)}, protocol.JSONField{Name: "teamName"}, protocol.JSONField{Name: "agentName"}, protocol.JSONField{Name: "promptId"}, protocol.JSONField{Name: "agentId"})
		if compact {
			entry.Set("parentUuid", json.RawMessage("null"))
			entry.Set("logicalParentUuid", parent)
		}
		if text(message.Get("type")) == "user" {
			entry.Set("promptId", promptID)
		}
		entry.Assign(*message)
		entry.Set("userType", quote("external"))
		entry.Set("entrypoint", nil)
		if name := transcriptEntrypoint(writer.options.Environment); name != "" {
			entry.Set("entrypoint", quote(name))
		}
		entry.Set("cwd", quote(writer.options.CWD))
		entry.Set("sessionId", quote(writer.currentSessionID()))
		entry.Set("version", quote(writer.options.Version))
		entry.Set("gitBranch", nil)
		if branchErr == nil {
			entry.Set("gitBranch", quote(branch))
		}
		entry.Set("slug", writer.metadata.Get("slug"))
		if envelope := live[message]; envelope != nil {
			writer.liveEntries[&entry] = envelope
		}
		if err := writer.append(&entry); err != nil {
			delete(writer.liveEntries, &entry)
			return nil, err
		}
		parent = message.Get("uuid")
	}
	if !writer.skipped() {
		for _, message := range newMessages {
			if text(message.Get("type")) != "user" || truthy(message.Get("isMeta")) || truthy(message.Get("isCompactSummary")) {
				continue
			}
			content := object(message.Get("message")).Get("content")
			var units []uint16
			if len(bytes.TrimSpace(content)) > 0 && bytes.TrimSpace(content)[0] == '"' {
				units, _ = protocol.DecodeJavaScriptJSONString(content)
			} else {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(content, &blocks)
				for _, block := range blocks {
					if text(block.Get("type")) == "text" && truthy(block.Get("text")) {
						units, _ = protocol.DecodeJavaScriptJSONString(block.Get("text"))
						break
					}
				}
			}
			if len(units) == 0 {
				continue
			}
			for index, unit := range units {
				if unit == '\n' {
					units[index] = ' '
				}
			}
			units = trimUnits(units)
			if len(units) > 200 {
				units = append(trimUnits(units[:200]), '…')
			}
			writer.lastPrompt = protocol.QuoteJavaScriptUTF16(units)
			break
		}
	}
	return parent, nil
}
func whitespace(unit uint16) bool {
	switch unit {
	case 9, 10, 11, 12, 13, 32, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return unit >= 0x2000 && unit <= 0x200a
}
func trimUnits(units []uint16) []uint16 {
	for len(units) > 0 && whitespace(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && whitespace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return units
}
func (writer *Writer) refreshMetadata(allowCreate bool) error {
	return writer.refreshMetadataWithTitleRefresh(allowCreate, false)
}

func (writer *Writer) refreshMetadataWithTitleRefresh(allowCreate, skipTitleRefresh bool) error {
	if writer.path == "" || writer.skipped() {
		return nil
	}
	// Only title/tag's exact top-level entries refresh cached mutable fields.
	if !skipTitleRefresh {
		if data, err := writer.options.Files.ReadFile(writer.path); err == nil {
			if len(data) > 65536 {
				data = data[len(data)-65536:]
			}
			lines := strings.Split(string(data), "\n")
			for _, field := range []struct{ kind, name string }{{"custom-title", "customTitle"}, {"tag", "tag"}} {
				for index := len(lines) - 1; index >= 0; index-- {
					if strings.HasPrefix(lines[index], `{"type":"`+field.kind+`"`) {
						value := object([]byte(lines[index])).Get(field.name)
						if len(value) > 0 {
							writer.metadata.Set(field.name, value)
						}
						break
					}
				}
			}
		}
	}

	entries := []protocol.JSONObject{}
	if truthy(writer.lastPrompt) {
		entries = append(entries, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("last-prompt")}, protocol.JSONField{Name: "lastPrompt", Value: writer.lastPrompt}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())}))
	}
	for _, field := range []struct{ kind, name string }{{"custom-title", "customTitle"}, {"tag", "tag"}, {"agent-name", "agentName"}, {"agent-color", "agentColor"}, {"agent-setting", "agentSetting"}, {"mode", "mode"}, {"worktree-state", "worktreeSession"}} {
		value := writer.metadata.Get(field.name)
		if !truthy(value) && !(field.name == "worktreeSession" && value != nil) {
			continue
		}
		entries = append(entries, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(field.kind)}, protocol.JSONField{Name: field.name, Value: value}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())}))
	}
	if writer.metadata.Get("prNumber") != nil && truthy(writer.metadata.Get("prUrl")) && truthy(writer.metadata.Get("prRepository")) {
		entries = append(entries, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("pr-link")}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())}, protocol.JSONField{Name: "prNumber", Value: writer.metadata.Get("prNumber")}, protocol.JSONField{Name: "prUrl", Value: writer.metadata.Get("prUrl")}, protocol.JSONField{Name: "prRepository", Value: writer.metadata.Get("prRepository")}, protocol.JSONField{Name: "timestamp", Value: quote(writer.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}))
	}
	for _, entry := range entries {
		flags := os.O_WRONLY | os.O_APPEND
		if allowCreate {
			flags |= os.O_CREATE
		}
		file, err := writer.options.Files.OpenFile(writer.path, flags, 0600)
		if errors.Is(err, fs.ErrNotExist) && !allowCreate {
			continue
		}
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if !allowCreate && runtime.GOOS != "windows" {
			if chmodErr := file.Chmod(0600); err == nil {
				err = chmodErr
			}
		}
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular append target: " + writer.path)
		}
		encoded, _ := entry.MarshalJSON()
		encoded, serializationErr := protocol.ParseAndStringifyJavaScriptJSON(encoded)
		if err == nil {
			err = serializationErr
		}
		if err == nil {
			err = writeAll(file, append(encoded, '\n'))
		}
		closed := file.Close()
		if err == nil {
			err = closed
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (writer *Writer) RefreshMetadata() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.refreshMetadata(false)
}
func utf16Text(units []uint16) string { return string(utf16.Decode(units)) }
