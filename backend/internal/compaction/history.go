package compaction

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sort"
)

func groupRounds(messages []protocol.JSONObject) [][]protocol.JSONObject {
	groups := [][]protocol.JSONObject{}
	current := []protocol.JSONObject{}
	last := ""
	for _, m := range messages {
		api, _ := m.Object("message")
		id := api.String("id")
		if m.String("type") == "assistant" && id != last && len(current) > 0 {
			groups = append(groups, current)
			current = []protocol.JSONObject{m}
		} else {
			current = append(current, m)
		}
		if m.String("type") == "assistant" {
			last = id
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}
func discoveredTools(messages []protocol.JSONObject) []string {
	seen := map[string]bool{}
	var walk func(json.RawMessage)
	walk = func(raw json.RawMessage) {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			for _, v := range arr {
				walk(v)
			}
			return
		}
		var object protocol.JSONObject
		if object.UnmarshalJSON(raw) != nil || !object.IsObject() {
			return
		}
		if object.String("type") == "tool_reference" && object.String("tool_name") != "" {
			seen[object.String("tool_name")] = true
		}
		for _, f := range object.OwnProperties() {
			walk(f.Value)
		}
	}
	for _, m := range messages {
		data, _ := m.MarshalJSON()
		walk(data)
	}
	out := []string{}
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
