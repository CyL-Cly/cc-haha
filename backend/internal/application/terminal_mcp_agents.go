package application

import (
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
)

func terminalMCPListItems(servers, agents []protocol.JSONObject) ([]protocol.JSONObject, error) {
	comparator, err := sourcecollation.New()
	if err != nil {
		return nil, err
	}
	defer comparator.Close()
	compare := func(a, b protocol.JSONObject) bool {
		left, _ := protocol.DecodeJavaScriptJSONString(a.Get("name"))
		right, _ := protocol.DecodeJavaScriptJSONString(b.Get("name"))
		return comparator.Compare(left, right) < 0
	}
	groups := map[string][]protocol.JSONObject{}
	for _, server := range servers {
		if server.String("name") == "ide" {
			continue
		}
		group := server.String("scope")
		if obj(server.Get("config")).String("type") == "claudeai-proxy" {
			group = "claudeai"
		}
		groups[group] = append(groups[group], server.Clone())
	}
	for _, agent := range agents {
		row := agent.Clone()
		row.Set("agentOnly", raw(true))
		row.Set("status", raw("not connected (agent-only)"))
		row.Set("scope", raw("agent"))
		row.Set("config", raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: row.Get("transport")}, protocol.JSONField{Name: "url", Value: row.Get("url")}, protocol.JSONField{Name: "command", Value: row.Get("command")})))
		groups["agent"] = append(groups["agent"], row)
	}
	rows := []protocol.JSONObject{}
	for _, scope := range []string{"project", "local", "user", "enterprise", "claudeai", "agent", "dynamic"} {
		group := groups[scope]
		sort.SliceStable(group, func(i, j int) bool { return compare(group[i], group[j]) })
		rows = append(rows, group...)
	}
	return rows, nil
}
