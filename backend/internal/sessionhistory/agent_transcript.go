package sessionhistory

import (
	"encoding/json"
	"math"
)

// AgentTranscript is the original sidechain chain, not a desktop projection.
type AgentTranscript struct {
	Messages            []json.RawMessage
	ContentReplacements []json.RawMessage
}

// LoadAgentTranscript ports getAgentTranscript against the same CLI loader.
// Source treats any file/chain failure and a missing matching leaf as absent.
func (loader *Loader) LoadAgentTranscript(path, agentID string) *AgentTranscript {
	value, err := loader.LoadTranscriptFile(path, false)
	if err != nil {
		return nil
	}
	parents := map[string]bool{}
	rows := []record{}
	for _, row := range value.records() {
		if row.value.String("agentId") == agentID && truthy(row.value.Get("isSidechain")) {
			rows = append(rows, row)
			parents[row.parent] = true
		}
	}
	var tip *record
	maximum := math.Inf(-1)
	for _, row := range rows {
		if parents[row.uuid] {
			continue
		}
		date, valid, err := loader.date(row.value.Get("timestamp"))
		if err != nil {
			return nil
		}
		if valid && date > maximum {
			copy := row
			tip, maximum = &copy, date
		}
	}
	if tip == nil {
		return nil
	}
	chain, err := loader.chain(value, *tip)
	if err != nil {
		return nil
	}
	result := &AgentTranscript{Messages: []json.RawMessage{}, ContentReplacements: []json.RawMessage{}}
	for _, row := range chain {
		if row.value.String("agentId") != agentID {
			continue
		}
		copy := row.value.Clone()
		copy.Delete("parentUuid")
		copy.Delete("isSidechain")
		result.Messages = append(result.Messages, encoded(copy))
	}
	if raw, ok := value.MetadataValue("agentContentReplacements", quote(agentID)); ok {
		if json.Unmarshal(raw, &result.ContentReplacements) != nil {
			return nil
		}
	}
	return result
}
