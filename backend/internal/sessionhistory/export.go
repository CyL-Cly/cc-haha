package sessionhistory

import (
	"encoding/json"
	"errors"
)

// BuildConversationChain exposes the already proven CLI chain reducer to the
// resume orchestrator. The tip must belong to this independently loaded file.
// Raw retains persistence fields for snapshot association; Messages applies
// Source removeExtraFields to the same chain.
func (loader *Loader) BuildConversationChain(value *Transcript, tip json.RawMessage) (raw, messages []json.RawMessage, err error) {
	if value == nil {
		return nil, nil, errors.New("session history chain requires a loaded transcript")
	}
	leaf, exists := value.messages[key(tip, -1)]
	if !exists {
		return nil, nil, errors.New("session history chain tip is not in the loaded transcript")
	}
	chain, err := loader.chain(value, leaf)
	if err != nil {
		return nil, nil, err
	}
	raw, messages = []json.RawMessage{}, []json.RawMessage{}
	for _, entry := range chain {
		raw = append(raw, encoded(entry.value))
		copy := entry.value.Clone()
		copy.Delete("parentUuid")
		copy.Delete("isSidechain")
		messages = append(messages, encoded(copy))
	}
	return raw, messages, nil
}

// MetadataValue reads the actual persisted map rather than the JSON display
// projection. A present JavaScript undefined value stays nil; JSON null stays
// the literal "null". Observer-owned bytes cannot mutate the loaded transcript.
func (value *Transcript) MetadataValue(name string, rawKey json.RawMessage) (json.RawMessage, bool) {
	if value == nil {
		return nil, false
	}
	values, exists := value.maps[name]
	if !exists {
		return nil, false
	}
	pair, exists := values.pairs[key(rawKey, -1)]
	if !exists {
		return nil, false
	}
	return append(json.RawMessage(nil), pair.Value...), true
}
