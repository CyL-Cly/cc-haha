package toolsearch

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceDeferredToolDeltaDisconnectUndeferReaddCompactAndUTF16(t *testing.T) {
	tools := []protocol.JSONObject{input(`{"name":"ToolSearch"}`), input(`{"name":"Read"}`), input(`{"name":"NotebookEdit","shouldDefer":true}`), input(`{"name":"mcp__z__read","isMcp":true}`), input(`{"name":"mcp__always__read","isMcp":true,"alwaysLoad":true}`), input(`{"name":"mcp__\ud800__𐐀","isMcp":true}`)}
	histories := [][]protocol.JSONObject{
		{},
		{input(`{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["NotebookEdit","mcp__z__read","mcp__\ud800__𐐀"],"addedLines":[],"removedNames":[]}}`)},
		{input(`{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__z__read","gone","Read","mcp__always__read"],"addedLines":[],"removedNames":[]}}`)},
		{input(`{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["gone","mcp__z__read"],"addedLines":[],"removedNames":[]}}`), input(`{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__\ud800__𐐀"],"addedLines":[],"removedNames":["gone","mcp__z__read"]}}`)},
		{input(`{"type":"system","subtype":"compact_boundary","compactMetadata":{"preCompactDiscoveredTools":["mcp__z__read"]}}`), input(`{"type":"attachment","attachment":{"type":"skill_listing","content":"unrelated"}}`)},
	}
	rows := []map[string]any{}
	for _, history := range histories {
		rows = append(rows, map[string]any{"tools": tools, "history": history})
	}
	rows = append(rows, map[string]any{"tools": []protocol.JSONObject{tools[0], tools[1]}, "history": histories[2]})
	rows = append(rows, map[string]any{"tools": append(append([]protocol.JSONObject{}, tools...), tools[3]), "history": histories[0]})
	original := source(t, map[string]any{"poolRows": rows})
	var outputs []protocol.JSONObject
	if e := json.Unmarshal(original.Get("poolRows"), &outputs); e != nil || len(outputs) != len(rows) {
		t.Fatal(e, len(outputs))
	}
	for i, row := range rows {
		native := []Tool{}
		for _, d := range row["tools"].([]protocol.JSONObject) {
			native = append(native, Tool{Name: d.Get("name"), IsMCP: string(d.Get("isMcp")) == "true", AlwaysLoad: string(d.Get("alwaysLoad")) == "true", ShouldDefer: string(d.Get("shouldDefer")) == "true"})
		}
		history := row["history"].([]protocol.JSONObject)
		before, _ := json.Marshal(history)
		delta, e := DeferredToolsDelta(native, history)
		if e != nil {
			t.Fatal(i, e)
		}
		if !equalJSON(raw(delta), outputs[i].Get("delta")) {
			t.Errorf("delta%d Go=%s Source=%s", i, raw(delta), outputs[i].Get("delta"))
		}
		var expected []json.RawMessage
		_ = json.Unmarshal(outputs[i].Get("discovered"), &expected)
		actual := Discovered(history)
		want := map[string]bool{}
		for _, name := range expected {
			want[string(protocol.StringifyValidatedJavaScriptJSON(name))] = true
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("discovered%d %v/%v", i, actual, want)
		}
		after, _ := json.Marshal(history)
		if string(before) != string(after) {
			t.Fatal("delta mutated retained history", i)
		}
	}
}
