package sessionresume

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FilterSubagent uses exactly resumeAgentBackground's three filters. It does
// not perform main-session interruption recovery or synthesize tool results.
func (reducer *Reducer) FilterSubagent(raws []json.RawMessage) ([]protocol.JSONObject, error) {
	rows, err := ParseMessages(raws)
	if err == nil {
		rows, err = unresolved(rows)
	}
	if err == nil {
		rows, err = reducer.thinking(rows)
	}
	if err == nil {
		rows, err = reducer.whitespace(rows)
	}
	if err != nil {
		return nil, err
	}
	out := make([]protocol.JSONObject, 0, len(rows))
	for _, row := range rows {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON(encoded(row.value)); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
