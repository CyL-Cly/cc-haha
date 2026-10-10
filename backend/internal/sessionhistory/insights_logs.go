package sessionhistory

import (
	"encoding/json"
	"errors"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FullLog is Source loadAllLogsFromSessionFile's canonical branch data. These
// records come from the existing transcript reducer and conversation chain.
type FullLog struct {
	Messages                                               []protocol.JSONObject
	SessionID, LeafUUID, ProjectPath, FirstPrompt, Summary json.RawMessage
	CreatedMillis, ModifiedMillis                          float64
}

type AllLogsOptions struct {
	FirstPrompt         func([]protocol.JSONObject) (json.RawMessage, error)
	ProjectPathOverride *string
}

func (loader *Loader) AllLogs(path string, o AllLogsOptions) ([]FullLog, error) {
	if o.FirstPrompt == nil {
		return nil, errors.New("all session logs require the original first-prompt producer")
	}
	transcript, err := loader.LoadTranscriptFile(path, true)
	if err != nil {
		return nil, err
	}
	if len(transcript.messages) == 0 {
		return []FullLog{}, nil
	}
	leaves := map[string]bool{}
	for _, name := range transcript.Leaves {
		leaves[name] = true
	}
	children := map[string][]record{}
	tips := []record{}
	for _, record := range transcript.records() {
		if leaves[record.uuid] {
			tips = append(tips, record)
		} else if truthy(record.value.Get("parentUuid")) {
			children[record.parent] = append(children[record.parent], record)
		}
	}
	logs := []FullLog{}
	for _, tip := range tips {
		chain, err := loader.chain(transcript, tip)
		if err != nil {
			return nil, err
		}
		if len(chain) == 0 {
			continue
		}
		if trailing := children[tip.uuid]; len(trailing) > 0 {
			if err = loader.sortByTimestamp(trailing); err != nil {
				return nil, err
			}
			chain = append(chain, trailing...)
		}
		records := make([]protocol.JSONObject, len(chain))
		for i, record := range chain {
			records[i] = record.value.Clone()
		}
		first, err := o.FirstPrompt(records)
		if err != nil {
			return nil, err
		}
		created, valid, err := loader.date(chain[0].value.Get("timestamp"))
		if err != nil {
			return nil, err
		}
		if !valid {
			created = math.NaN()
		}
		modified, valid, err := loader.date(tip.value.Get("timestamp"))
		if err != nil {
			return nil, err
		}
		if !valid {
			modified = math.NaN()
		}
		project := chain[0].value.Get("cwd")
		if o.ProjectPathOverride != nil {
			project = quote(*o.ProjectPathOverride)
		}
		summary := transcript.maps["summaries"].pairs[tip.uuid].Value
		for _, record := range records {
			record.Delete("isSidechain")
			record.Delete("parentUuid")
		}
		logs = append(logs, FullLog{Messages: records, SessionID: tip.value.Get("sessionId"), LeafUUID: tip.value.Get("uuid"), ProjectPath: project, FirstPrompt: first, Summary: summary, CreatedMillis: created, ModifiedMillis: modified})
	}
	return logs, nil
}
