package toolsearch

import (
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type DeferredDelta struct {
	AddedNames   []json.RawMessage `json:"addedNames"`
	AddedLines   []json.RawMessage `json:"addedLines"`
	RemovedNames []json.RawMessage `json:"removedNames"`
}

func DeltaEnabled(inputs GateInputs) bool {
	return inputs.Environment["USER_TYPE"] == "ant" || inputs.DeltaEnabled != nil && inputs.DeltaEnabled()
}

// DeferredToolsDelta reconstructs announcement state from original retained
// attachments. A tool that is still in the pool but no longer deferred is
// silent, rather than falsely announced as disconnected.
func DeferredToolsDelta(tools []Tool, history []protocol.JSONObject) (*DeferredDelta, error) {
	announced := map[string]json.RawMessage{}
	for _, record := range history {
		if record.String("type") != "attachment" {
			continue
		}
		attachment, ok := record.Object("attachment")
		if !ok {
			return nil, &CapabilityError{"malformed deferred-tools attachment scan"}
		}
		if attachment.String("type") != "deferred_tools_delta" {
			continue
		}
		for _, field := range []string{"addedNames", "removedNames"} {
			var names []json.RawMessage
			if e := json.Unmarshal(attachment.Get(field), &names); e != nil || names == nil {
				return nil, &CapabilityError{"malformed deferred-tools announcement names"}
			}
			for _, name := range names {
				if _, e := protocol.DecodeJavaScriptJSONString(name); e != nil {
					return nil, &CapabilityError{"non-string retained deferred-tools name"}
				}
				key := string(protocol.StringifyValidatedJavaScriptJSON(name))
				if field == "addedNames" {
					announced[key] = append(json.RawMessage(nil), name...)
				} else {
					delete(announced, key)
				}
			}
		}
	}
	delta := &DeferredDelta{AddedNames: []json.RawMessage{}, AddedLines: []json.RawMessage{}, RemovedNames: []json.RawMessage{}}
	pool, deferred := map[string]bool{}, map[string]bool{}
	for _, tool := range tools {
		key := string(protocol.StringifyValidatedJavaScriptJSON(tool.Name))
		pool[key] = true
		if !IsDeferred(tool) {
			continue
		}
		deferred[key] = true
		if _, exists := announced[key]; !exists {
			delta.AddedNames = append(delta.AddedNames, tool.Name)
			delta.AddedLines = append(delta.AddedLines, tool.Name)
		}
	}
	for key, name := range announced {
		if !deferred[key] && !pool[key] {
			delta.RemovedNames = append(delta.RemovedNames, name)
		}
	}
	if len(delta.AddedNames) == 0 && len(delta.RemovedNames) == 0 {
		return nil, nil
	}
	for _, names := range [][]json.RawMessage{delta.AddedNames, delta.AddedLines, delta.RemovedNames} {
		sort.SliceStable(names, func(i, j int) bool { return less(units(names[i]), units(names[j])) })
	}
	return delta, nil
}
