package sessionhistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type Diagnostic struct {
	Code, Message string
	Fields        json.RawMessage
}
type Options struct {
	Files                 sessionstore.FileSystem
	Stat                  func(string) (int64, error)
	ReadFile              func(string) ([]byte, error)
	DisablePrecompactSkip func() bool
	LeafPrune             func() bool
	ParseDate             sessionprojection.DateParser
	Compare               StringComparator
	OnDiagnostic          func(Diagnostic)
}
type Loader struct{ options Options }

func New(options Options) (*Loader, error) {
	if options.Files == nil || options.Stat == nil || options.ReadFile == nil || options.DisablePrecompactSkip == nil || options.LeafPrune == nil || options.ParseDate == nil || options.Compare == nil || options.OnDiagnostic == nil {
		return nil, errors.New("session history requires real files, stat/read, skip/prune policies, date/collation and diagnostics")
	}
	return &Loader{options}, nil
}

type record struct {
	value        protocol.JSONObject
	uuid, parent string
}
type Transcript struct {
	order                   []string
	messages                map[string]record
	messageKeys             map[string]MessageKey
	maps                    map[string]*orderedMap
	ContextCollapseCommits  []json.RawMessage
	ContextCollapseSnapshot json.RawMessage
	Leaves                  []string
}

var metadataNames = []string{"summaries", "customTitles", "tags", "agentNames", "agentColors", "agentSettings", "prNumbers", "prUrls", "prRepositories", "modes", "worktreeStates", "fileHistorySnapshots", "attributionSnapshots", "contentReplacements", "agentContentReplacements"}

func newTranscript() *Transcript {
	value := &Transcript{messages: map[string]record{}, messageKeys: map[string]MessageKey{}, maps: map[string]*orderedMap{}, ContextCollapseCommits: []json.RawMessage{}, Leaves: []string{}}
	for _, name := range metadataNames {
		result := newMap()
		value.maps[name] = &result
	}
	return value
}
func (value *Transcript) put(message record) {
	if _, exists := value.messages[message.uuid]; !exists {
		value.order = append(value.order, message.uuid)
		value.messageKeys[message.uuid] = MessageKey{identity: message.uuid, raw: append(json.RawMessage(nil), message.value.Get("uuid")...)}
	}
	value.messages[message.uuid] = message
}
func (value *Transcript) records() []record {
	result := []record{}
	for _, name := range value.order {
		if message, exists := value.messages[name]; exists {
			result = append(result, message)
		}
	}
	return result
}
func (value *Transcript) MarshalJSON() ([]byte, error) {
	fields := protocol.NewJSONObject()
	items := newMap()
	for _, entry := range value.records() {
		items.set(entry.value.Get("uuid"), encoded(entry.value))
	}
	raw, err := items.MarshalJSON()
	if err != nil {
		return nil, err
	}
	fields.Set("messages", raw)
	for _, name := range metadataNames {
		raw, err = value.maps[name].MarshalJSON()
		if err != nil {
			return nil, err
		}
		fields.Set(name, raw)
	}
	raw, err = json.Marshal(value.ContextCollapseCommits)
	if err != nil {
		return nil, err
	}
	fields.Set("contextCollapseCommits", raw)
	fields.Set("contextCollapseSnapshot", value.ContextCollapseSnapshot)
	leaves := []json.RawMessage{}
	for _, name := range value.Leaves {
		raw := value.messages[name].value.Get("uuid")
		if raw == nil {
			raw = json.RawMessage("null")
		}
		leaves = append(leaves, raw)
	}
	raw, err = json.Marshal(leaves)
	if err != nil {
		return nil, err
	}
	fields.Set("leafUuids", raw)
	return fields.MarshalJSON()
}
func parseLines(data []byte) []json.RawMessage {
	result := []json.RawMessage{}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(protocol.DecodeJavaScriptUTF8(line))
		if len(line) > 0 && json.Valid(line) {
			result = append(result, append(json.RawMessage(nil), line...))
		}
	}
	return result
}
func (value *Transcript) metadata(entry protocol.JSONObject) bool {
	var mapName, mapKey, property string
	switch kind(entry) {
	case "summary":
		mapName, mapKey, property = "summaries", "leafUuid", "summary"
	case "custom-title":
		mapName, mapKey, property = "customTitles", "sessionId", "customTitle"
	case "tag":
		mapName, mapKey, property = "tags", "sessionId", "tag"
	case "agent-name":
		mapName, mapKey, property = "agentNames", "sessionId", "agentName"
	case "agent-color":
		mapName, mapKey, property = "agentColors", "sessionId", "agentColor"
	case "agent-setting":
		mapName, mapKey, property = "agentSettings", "sessionId", "agentSetting"
	case "mode":
		mapName, mapKey, property = "modes", "sessionId", "mode"
	case "worktree-state":
		mapName, mapKey, property = "worktreeStates", "sessionId", "worktreeSession"
	case "pr-link":
		if truthy(entry.Get("sessionId")) {
			for _, pair := range [][2]string{{"prNumbers", "prNumber"}, {"prUrls", "prUrl"}, {"prRepositories", "prRepository"}} {
				value.maps[pair[0]].set(entry.Get("sessionId"), entry.Get(pair[1]))
			}
		}
		return true
	default:
		return false
	}
	if truthy(entry.Get(mapKey)) {
		value.maps[mapName].set(entry.Get(mapKey), entry.Get(property))
	}
	return true
}

// LoadTranscriptFile preserves Source's read-failure/partially processed load
// behavior. The loader does not rewrite the append-only transcript. It uses
// the CLI's own precompact semantics rather than server display/record limits.
func (loader *Loader) LoadTranscriptFile(path string, keepAllLeaves bool) (*Transcript, error) {
	if !utf8.ValidString(path) {
		return nil, sessionprojection.ErrUnsupportedPath
	}
	data, metadata, err := loader.read(path, keepAllLeaves)
	return loader.reduceTranscript(data, metadata, err)
}

func (loader *Loader) reduceTranscript(data, metadata []byte, readError error) (*Transcript, error) {
	value := newTranscript()
	if readError == nil {
		for _, raw := range parseLines(metadata) {
			if isNull(raw) {
				break
			}
			value.metadata(object(raw))
		}
		bridge := map[string]json.RawMessage{}
		for _, raw := range parseLines(data) {
			if isNull(raw) {
				break
			}
			entry := object(raw)
			uuid, parent := entry.Get("uuid"), entry.Get("parentUuid")
			identity := len(value.order) + len(bridge)
			name, parentName := key(uuid, identity), key(parent, -1)
			if kind(entry) == "progress" && len(uuid) > 0 && uuid[0] == '"' {
				if previous, exists := bridge[parentName]; truthy(parent) && exists {
					parent = previous
					if parent == nil {
						parent = json.RawMessage("null")
					}
				}
				bridge[name] = parent
				continue
			}
			switch kind(entry) {
			case "user", "assistant", "attachment", "system":
				if previous, exists := bridge[parentName]; truthy(parent) && exists {
					parent = previous
					if parent == nil {
						parent = json.RawMessage("null")
					}
					entry.Set("parentUuid", parent)
					parentName = key(parent, -1)
				}
				value.put(record{entry, name, parentName})
				if compact(entry) {
					value.ContextCollapseCommits = []json.RawMessage{}
					value.ContextCollapseSnapshot = nil
				}
			case "file-history-snapshot":
				value.maps["fileHistorySnapshots"].set(entry.Get("messageId"), encoded(entry))
			case "attribution-snapshot":
				value.maps["attributionSnapshots"].set(entry.Get("messageId"), encoded(entry))
			case "content-replacement":
				mapName, mapKey := "contentReplacements", entry.Get("sessionId")
				if truthy(entry.Get("agentId")) {
					mapName, mapKey = "agentContentReplacements", entry.Get("agentId")
				}
				values := value.maps[mapName]
				prior, _ := array(values.pairs[key(mapKey, -1)].Value)
				if prior == nil {
					prior = []json.RawMessage{}
				}
				initial, _ := json.Marshal(prior)
				values.set(mapKey, initial)
				items, valid := iterable(entry.Get("replacements"))
				if !valid {
					goto end
				}
				prior = append(prior, items...)
				combined, _ := json.Marshal(prior)
				values.set(mapKey, combined)
			case "marble-origami-commit":
				value.ContextCollapseCommits = append(value.ContextCollapseCommits, encoded(entry))
			case "marble-origami-snapshot":
				value.ContextCollapseSnapshot = encoded(entry)
			default:
				value.metadata(entry)
			}
		}
	}
end:
	if err := loader.preserved(value); err != nil {
		return nil, err
	}
	if err := loader.snip(value); err != nil {
		return nil, err
	}
	loader.leaves(value)
	return value, nil
}

type ResumeResult struct {
	Messages  []json.RawMessage `json:"messages"`
	SessionID json.RawMessage   `json:"sessionId,omitempty"`
}

// LoadMessagesFromJSONLPath is the real CLI explicit-JSONL resume entry point.
// Fork identity comes from the selected leaf, not the copied root record.
func (loader *Loader) LoadMessagesFromJSONLPath(path string) (ResumeResult, error) {
	value, err := loader.LoadTranscriptFile(path, false)
	if err != nil {
		return ResumeResult{}, err
	}
	return loader.SelectJSONLMessages(value)
}

// SelectJSONLMessages is shared by synchronous and owner-bound explicit-file
// resume. The physical reader remains separate from original leaf selection.
func (loader *Loader) SelectJSONLMessages(value *Transcript) (ResumeResult, error) {
	leaves := map[string]bool{}
	for _, name := range value.Leaves {
		leaves[name] = true
	}
	var tip *record
	maximum := float64(0)
	for _, message := range value.records() {
		if truthy(message.value.Get("isSidechain")) || !leaves[message.uuid] {
			continue
		}
		raw := message.value.Get("timestamp")
		millis, valid, err := loader.date(raw)
		if err != nil {
			return ResumeResult{}, err
		}
		if valid && millis > maximum {
			copy := message
			tip, maximum = &copy, millis
		}
	}
	if tip == nil {
		return ResumeResult{Messages: []json.RawMessage{}}, nil
	}
	chain, err := loader.chain(value, *tip)
	if err != nil {
		return ResumeResult{}, err
	}
	result := ResumeResult{Messages: []json.RawMessage{}, SessionID: tip.value.Get("sessionId")}
	for _, message := range chain {
		raw := message.value.Clone()
		raw.Delete("parentUuid")
		raw.Delete("isSidechain")
		result.Messages = append(result.Messages, encoded(raw))
	}
	return result, nil
}
