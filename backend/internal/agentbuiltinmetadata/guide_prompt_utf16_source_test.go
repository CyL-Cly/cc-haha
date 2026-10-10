package agentbuiltinmetadata

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestGuidePromptQuotedJSONMatchesActualSourceDynamicUTF16AndOrdinaryBranches(t *testing.T) {
	parse := func(raw string) protocol.JSONObject {
		var result protocol.JSONObject
		if err := result.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		return result
	}
	contexts := []GuideContext{
		{},
		{Commands: []protocol.JSONObject{parse(`{"type":"prompt","source":"plugin","name":"guide-\ud800:entry","description":"desc-\udc00"}`)}},
		{Commands: []protocol.JSONObject{parse(`{"type":"prompt","name":"ordinary","description":"ordinary description"}`), parse(`{"type":"local","name":"ignored","description":"ignored"}`)}},
		{Agents: []protocol.JSONObject{parse(`{"agentType":"agent-\ud800","whenToUse":"use-\udc00","source":"projectSettings"}`), parse(`{"agentType":"ignored","source":"built-in"}`)}},
		{MCPServers: []protocol.JSONObject{parse(`{"name":"mcp-\udc00"}`)}},
		{Commands: []protocol.JSONObject{parse(`{"type":"prompt"}`), parse(`{"type":"prompt","name":42,"description":false}`)}},
		{Using3P: true, IssuesExplainer: "https://fixture.invalid/feedback"},
	}
	cases := []any{}
	for _, context := range contexts {
		cases = append(cases, map[string]any{"action": "guide", "commands": context.Commands, "agents": context.Agents, "mcpServers": context.MCPServers, "using3P": context.Using3P, "issuesExplainer": context.IssuesExplainer})
	}
	for _, settingsInput := range []protocol.JSONObject{parse(`{}`), parse(`{"language":"zh-CN","future":{"keep":"\ud800"}}`)} {
		observed := source(t, map[string]any{"settings": settingsInput, "cases": cases})
		for i, raw := range observed {
			var actual protocol.JSONObject
			if err := actual.UnmarshalJSON(raw); err != nil {
				t.Fatal(err)
			}
			settings, _ := actual.Object("settings")
			context := contexts[i]
			context.Settings = settings
			quoted, err := GuidePromptJSON(context)
			if err != nil {
				t.Fatal(err)
			}
			want, err := protocol.DecodeJavaScriptJSONString(actual.Get("prompt"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := protocol.DecodeJavaScriptJSONString(quoted)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("actual Source guide formatter units differ case%d\nSource%s\nGo%s", i, actual.Get("prompt"), quoted)
			}
			var display string
			_ = json.Unmarshal(quoted, &display)
			if display != GuidePrompt(context) {
				t.Fatal("legacy display formatting changed", i)
			}
		}
	}
}
