package mcpconfig

import (
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
)

// AgentServers projects inline declarations without connecting them. The first
// declaration owns config while later agents contribute unique source names.
func AgentServers(agents []protocol.JSONObject) ([]protocol.JSONObject, error) {
	order := []string{}
	configs := map[string]protocol.JSONObject{}
	sources := map[string][]json.RawMessage{}
	for _, agent := range agents {
		var specs []json.RawMessage
		_ = json.Unmarshal(agent.Get("mcpServers"), &specs)
		for _, spec := range specs {
			var config protocol.JSONObject
			if config.UnmarshalJSON(spec) != nil || !config.IsObject() {
				continue
			}
			entries := config.OwnProperties()
			if len(entries) != 1 {
				continue
			}
			entry := entries[0]
			name, _ := protocol.DecodeJavaScriptJSONString(entry.NameJSON)
			key := string(protocol.QuoteJavaScriptUTF16(name))
			if _, found := configs[key]; !found {
				first := object(entry.Value)
				first.Set("name", entry.NameJSON)
				configs[key] = first
				order = append(order, key)
			}
			present := false
			for _, source := range sources[key] {
				if agentServerSourceKey(source) == agentServerSourceKey(agent.Get("agentType")) {
					present = true
					break
				}
			}
			if !present {
				sources[key] = append(sources[key], agent.Get("agentType"))
			}
		}
	}
	rows := []protocol.JSONObject{}
	for _, key := range order {
		config := configs[key]
		kind := config.String("type")
		if config.Get("type") == nil {
			kind = "stdio"
		}
		if kind != "stdio" && kind != "http" && kind != "sse" && kind != "ws" {
			continue
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: config.Get("name")}, protocol.JSONField{Name: "sourceAgents", Value: encode(sources[key])}, protocol.JSONField{Name: "transport", Value: encode(kind)})
		if kind == "stdio" {
			row.Set("command", config.Get("command"))
		} else {
			row.Set("url", config.Get("url"))
		}
		row.Set("needsAuth", encode(kind == "http" || kind == "sse"))
		rows = append(rows, row)
	}
	comparator, err := sourcecollation.New()
	if err != nil {
		return nil, err
	}
	defer comparator.Close()
	sort.SliceStable(rows, func(i, j int) bool {
		left, _ := protocol.DecodeJavaScriptJSONString(rows[i].Get("name"))
		right, _ := protocol.DecodeJavaScriptJSONString(rows[j].Get("name"))
		return comparator.Compare(left, right) < 0
	})
	return rows, nil
}

func agentServerSourceKey(value json.RawMessage) string {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return string(value)
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}
