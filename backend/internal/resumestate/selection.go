package resumestate

import (
	"encoding/json"
	"math"
	"path/filepath"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

func sanitizePath(path string) string {
	result := []byte{}
	for _, unit := range utf16.Encode([]rune(path)) {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' {
			result = append(result, byte(unit))
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}
func (service *Service) sanitizeProjectPath(path string) string {
	if service.options.NativeProjectPaths {
		return projectpath.Sanitize(path)
	}
	return sanitizePath(path)
}
func (service *Service) load(path string) (*sessionhistory.Transcript, protocol.JSONObject, error) {
	value, err := service.options.Loader.LoadTranscriptFile(path, false)
	if err != nil {
		return nil, protocol.JSONObject{}, err
	}
	raw, err := value.MarshalJSON()
	return value, object(raw), err
}
func pairs(raw json.RawMessage) [][2]json.RawMessage {
	var values [][2]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	return values
}
func lookup(data protocol.JSONObject, name string, key json.RawMessage) (json.RawMessage, bool) {
	for _, pair := range pairs(data.Get(name)) {
		if scalarEqual(pair[0], key) {
			return pair[1], true
		}
	}
	return nil, false
}
func (service *Service) latest(data protocol.JSONObject, full bool) (protocol.JSONObject, error) {
	maximum := math.Inf(-1)
	var tip protocol.JSONObject
	leaves, _ := array(data.Get("leafUuids"))
	for _, pair := range pairs(data.Get("messages")) {
		message := object(pair[1])
		if full {
			leaf := false
			for _, candidate := range leaves {
				if scalarEqual(candidate, message.Get("uuid")) {
					leaf = true
					break
				}
			}
			if !leaf || message.String("type") != "user" && message.String("type") != "assistant" {
				continue
			}
		} else if truthy(message.Get("isSidechain")) {
			continue
		}
		raw := message.Get("timestamp")
		raw, err := jsString(raw)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		value, _ := units(raw)
		milliseconds, valid, err := service.options.ParseDate(value)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if valid && milliseconds > maximum {
			maximum, tip = milliseconds, message
		}
	}
	if tip.IsObject() {
		raw := tip.Get("uuid")
		if len(raw) == 0 || raw[0] == '{' || raw[0] == '[' {
			return protocol.JSONObject{}, &CapabilityError{Capability: "non-scalar transcript chain-tip identity"}
		}
	}
	return tip, nil
}
func (service *Service) snapshots(data protocol.JSONObject, chain []json.RawMessage) []json.RawMessage {
	values := []json.RawMessage{}
	indices := []json.RawMessage{}
	for _, raw := range chain {
		entry, exists := lookup(data, "fileHistorySnapshots", object(raw).Get("uuid"))
		if !exists {
			continue
		}
		message := object(entry)
		snapshot := message.Get("snapshot")
		id := object(snapshot).Get("messageId")
		index := -1
		if truthy(message.Get("isSnapshotUpdate")) {
			for candidate, key := range indices {
				if scalarEqual(key, id) {
					index = candidate
				}
			}
		}
		if index < 0 {
			values = append(values, snapshot)
			indices = append(indices, id)
		} else {
			values[index] = snapshot
		}
	}
	return values
}
func metadataValues(data protocol.JSONObject, name string) []json.RawMessage {
	result := []json.RawMessage{}
	for _, pair := range pairs(data.Get(name)) {
		result = append(result, pair[1])
	}
	return result
}
func sessionFields(log *protocol.JSONObject, data protocol.JSONObject, sid json.RawMessage) {
	replacements, _ := lookup(data, "contentReplacements", sid)
	if replacements == nil {
		replacements = []byte("[]")
	}
	log.Set("contentReplacements", replacements)
	commits := []json.RawMessage{}
	items, _ := array(data.Get("contextCollapseCommits"))
	for _, item := range items {
		if scalarEqual(object(item).Get("sessionId"), sid) {
			commits = append(commits, item)
		}
	}
	log.Set("contextCollapseCommits", encode(commits))
	snapshot := data.Get("contextCollapseSnapshot")
	if !scalarEqual(object(snapshot).Get("sessionId"), sid) {
		snapshot = nil
	}
	log.Set("contextCollapseSnapshot", snapshot)
}
func (service *Service) lastLog(sid string) (protocol.JSONObject, error) {
	path := filepath.Join(service.projectDir(), sid+".jsonl")
	transcript, data, err := service.load(path)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	items := pairs(data.Get("messages"))
	if len(items) == 0 {
		return protocol.JSONObject{}, nil
	}
	service.mu.Lock()
	if _, exists := service.state.SessionUUIDs[sid]; !exists {
		service.uuidTranscripts[sid] = transcript
		service.state.SessionUUIDs[sid] = []json.RawMessage{}
		for _, item := range items {
			service.state.SessionUUIDs[sid] = append(service.state.SessionUUIDs[sid], item[0])
		}
	}
	service.mu.Unlock()
	tip, err := service.latest(data, false)
	if err != nil || !tip.IsObject() {
		return protocol.JSONObject{}, err
	}
	chain, messages, err := service.options.Loader.BuildConversationChain(transcript, tip.Get("uuid"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	log := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: encode(messages)})
	if sid == service.options.CurrentSessionID {
		log.Set("fullPath", quote(path))
	} else {
		log.Set("fullPath", quote(filepath.Join(service.options.ConfigDir, "projects", service.sanitizeProjectPath(service.options.OriginalCWD), sid+".jsonl")))
	}
	root := object(chain[0])
	log.Set("agentName", root.Get("agentName"))
	for _, name := range []string{"customTitle", "tag"} {
		source := name + "s"
		if name == "customTitle" {
			source = "customTitles"
		}
		value, _ := transcript.MetadataValue(source, tip.Get("sessionId"))
		log.Set(name, value)
	}
	setting, _ := transcript.MetadataValue("agentSettings", quote(sid))
	log.Set("agentSetting", setting)
	worktree, _ := transcript.MetadataValue("worktreeStates", quote(sid))
	log.Set("worktreeSession", worktree)
	log.Set("fileHistorySnapshots", encode(service.snapshots(data, chain)))
	log.Set("attributionSnapshots", encode(metadataValues(data, "attributionSnapshots")))
	sessionFields(&log, data, quote(sid))
	return log, nil
}
func (service *Service) fullLog(log protocol.JSONObject) (protocol.JSONObject, error) {
	messages, valid := array(log.Get("messages"))
	if !valid || len(messages) != 0 || log.Get("sessionId") == nil || !truthy(log.Get("fullPath")) {
		return log, nil
	}
	path := text(log.Get("fullPath"))
	transcript, data, err := service.load(path)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(pairs(data.Get("messages"))) == 0 {
		return log, nil
	}
	tip, err := service.latest(data, true)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !tip.IsObject() {
		return log, nil
	}
	chain, messages, err := service.options.Loader.BuildConversationChain(transcript, tip.Get("uuid"))
	if err != nil {
		// Source loadFullLog returns the original log on ordinary chain failure.
		return log, nil
	}
	log = log.Clone()
	log.Set("messages", encode(messages))
	sid := tip.Get("sessionId")
	for _, pair := range [][2]string{{"customTitle", "customTitles"}, {"tag", "tags"}, {"agentName", "agentNames"}, {"agentColor", "agentColors"}, {"agentSetting", "agentSettings"}, {"mode", "modes"}, {"prNumber", "prNumbers"}, {"prUrl", "prUrls"}, {"prRepository", "prRepositories"}} {
		if truthy(sid) {
			value, _ := transcript.MetadataValue(pair[1], sid)
			log.Set(pair[0], value)
		}
	}
	if truthy(sid) {
		if value, exists := transcript.MetadataValue("worktreeStates", sid); exists {
			log.Set("worktreeSession", value)
		}
		sessionFields(&log, data, sid)
	} else {
		log.Set("contextCollapseCommits", nil)
		log.Set("contextCollapseSnapshot", nil)
	}
	log.Set("fileHistorySnapshots", encode(service.snapshots(data, chain)))
	log.Set("attributionSnapshots", encode(metadataValues(data, "attributionSnapshots")))
	return log, nil
}

// UUIDTranscript retains the actual original loader Map for prime-if-absent.
// It never rebuilds a Set from selected/rendered conversation messages.
func (service *Service) UUIDTranscript(sid string) *sessionhistory.Transcript {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.uuidTranscripts[sid]
}
