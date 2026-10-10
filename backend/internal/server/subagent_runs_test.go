package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func subagentRow(t *testing.T, id, role, timestamp string, content any, extra map[string]any) string {
	t.Helper()
	message := map[string]any{"role": role, "content": content}
	entry := map[string]any{"type": role, "uuid": id, "timestamp": timestamp, "message": message}
	for key, value := range extra {
		if key == "usage" {
			message[key] = value
		} else {
			entry[key] = value
		}
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func writeSubagent(t *testing.T, info sessionInfo, id string, metadata map[string]any, rows ...string) {
	t.Helper()
	directory := subagentDirectory(info)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if metadata != nil {
		raw, _ := json.Marshal(metadata)
		if err := os.WriteFile(filepath.Join(directory, "agent-"+id+".meta.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(subagentPath(info, id), []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestSubagentDetailsReadNativeChildHistoryBeforeAndAfterResultWithoutOpeningHarness(t *testing.T) {
	opens := 0
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		opens++
		return &transportSession{}, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tool := map[string]any{"type": "tool_use", "id": "agent-call", "name": "Agent", "input": map[string]any{"description": "Physical child", "prompt": "write actual bytes"}}
	appendRows(t, info.Path, subagentRow(t, "parent-tool", "assistant", "2026-10-06T00:00:00.000Z", []any{tool}, nil))
	childTool := map[string]any{"type": "tool_use", "id": "write-child", "name": "Write", "input": map[string]any{"file_path": "child.txt", "content": "bytes"}}
	writeSubagent(t, info, "child-one", map[string]any{"toolUseId": "agent-call", "futureMetadata": true},
		subagentRow(t, "child-user", "user", "2026-10-06T00:00:01.000Z", "write actual bytes", nil),
		subagentRow(t, "child-tool", "assistant", "2026-10-06T00:00:02.000Z", []any{childTool}, map[string]any{"usage": map[string]any{"input_tokens": 12, "output_tokens": 8, "cache_read_input_tokens": 100}}))
	path := "/api/sessions/" + id + "/subagents/by-tool/agent-call"
	raw := requestJSON(t, host, "GET", path, "", 200)
	body := historyRecord(raw)
	if stringField(body, "status") != "running" || stringField(body, "agentId") != "child-one" || stringField(body, "source") != "subagent-jsonl" || string(body["historyComplete"]) != "true" || string(body["canSendMessage"]) != "false" || len(body["activityMessages"]) != 0 {
		t.Fatal(string(raw))
	}
	if string(historyRecord(body["usage"])["totalTokens"]) != "20" {
		t.Fatal("actual transcript usage not used", string(raw))
	}
	appendRows(t, subagentPath(info, "child-one"), subagentRow(t, "child-result", "user", "2026-10-06T00:00:03.000Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "write-child", "content": "created"}}, nil), subagentRow(t, "child-done", "assistant", "2026-10-06T00:00:04.000Z", []any{map[string]any{"type": "text", "text": "CHILD DONE"}}, nil))
	appendRows(t, info.Path, subagentRow(t, "parent-result", "user", "2026-10-06T00:00:05.000Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "agent-call", "content": "CHILD DONE\nagentId: child-one\n<usage>\ninput_tokens: 12\noutput_tokens: 8\n</usage>"}}, nil))
	raw = requestJSON(t, host, "GET", path, "", 200)
	body = historyRecord(raw)
	var messages []json.RawMessage
	_ = json.Unmarshal(body["messages"], &messages)
	if stringField(body, "status") != "completed" || stringField(body, "result") != "CHILD DONE" || len(messages) != 4 || stringField(body, "updatedAt") != "2026-10-06T00:00:05.000Z" {
		t.Fatal(string(raw))
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-tool/missing", "", 404)
	requestJSON(t, host, "POST", path, "{}", 405)
	requestJSON(t, host, "GET", path+"/messages", "", 405)
	if opens != 0 {
		t.Fatal("history card opened another Agent graph", opens)
	}
	requestJSON(t, host, "POST", path+"/messages", `{"content":"follow up"}`, 501)
	requestJSON(t, host, "GET", path+"/unknown", "", 404)
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-agent/child-one", "", 501)
	if opens != 1 {
		t.Fatal("send control must initialize the actual session once", opens)
	}
}

func TestNestedAgentOwnerJoinAndEncodedCanonicalRouteDoNotSelectAnotherParentsChild(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tool := map[string]any{"type": "tool_use", "id": "Agent:0", "name": "Agent", "input": map[string]any{"description": "owned nested", "prompt": "owner prompt"}}
	writeSubagent(t, info, "parent-a", map[string]any{}, subagentRow(t, "nested-tool", "assistant", "2026-10-06T00:00:00.000Z", []any{tool}, nil))
	writeSubagent(t, info, "child-a", map[string]any{"toolUseId": "Agent:0", "ownerAgentId": "parent-a"}, subagentRow(t, "child-a-user", "user", "2026-10-06T00:00:01.000Z", "A only", nil))
	writeSubagent(t, info, "child-b", map[string]any{"toolUseId": "Agent:0", "ownerAgentId": "parent-b"}, subagentRow(t, "child-b-user", "user", "2026-10-06T00:00:02.000Z", "B private", nil))
	appendRows(t, info.Path, `{"type":"cc-haha-task-notification","timestamp":"2026-10-06T00:00:03.000Z","taskNotification":{"toolUseId":"Agent:0","ownerAgentId":"parent-b","taskId":"child-b","status":"failed","result":"B failed"}}`)
	ref := "root-tool/parent-a/Agent:0"
	path := "/api/sessions/" + id + "/subagents/by-tool/" + url.PathEscape(ref)
	raw := requestJSON(t, host, "GET", path, "", 200)
	body := historyRecord(raw)
	if stringField(body, "toolUseId") != ref || stringField(body, "agentId") != "child-a" || stringField(body, "status") != "running" || !strings.Contains(string(body["messages"]), "A only") || strings.Contains(string(raw), "B private") || len(body["result"]) != 0 {
		t.Fatal(string(raw))
	}
	// Root ownership also must not accidentally pick a child owned elsewhere.
	appendRows(t, info.Path, subagentRow(t, "root-tool", "assistant", "2026-10-06T00:00:04.000Z", []any{tool}, nil))
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-tool/Agent:0", "", 200)
	if string(historyRecord(raw)["agentId"]) != "null" || strings.Contains(string(raw), "B private") {
		t.Fatal(string(raw))
	}
	// A broken metadata file invalidates only the legacy cross-owner fallback.
	writeSubagent(t, info, "legacy", map[string]any{"toolUseId": "legacy-call"}, subagentRow(t, "legacy-user", "user", "2026-10-06T00:00:05.000Z", "legacy", nil))
	legacy, err := server.metadataAgentID(context.Background(), info, "legacy-call", "parent-a")
	if err != nil || legacy != "legacy" {
		t.Fatal(legacy, err)
	}
	if err := os.WriteFile(filepath.Join(subagentDirectory(info), "agent-broken.meta.json"), []byte(`{"toolUseId":`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err = server.metadataAgentID(context.Background(), info, "legacy-call", "parent-a")
	if err != nil || legacy != "" {
		t.Fatal("incomplete metadata must not prove unique legacy owner", legacy, err)
	}
}

func TestSubagentTranscriptEvidenceBudgetAndActivityScoping(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tool := map[string]any{"type": "tool_use", "id": "long-run", "name": "Agent", "input": map[string]any{"description": "long child"}}
	appendRows(t, info.Path, subagentRow(t, "root-long", "assistant", "2026-10-06T00:00:00.000Z", []any{tool}, nil))
	rows := []string{}
	for i := 0; i < 1001; i++ {
		rows = append(rows, subagentRow(t, fmt.Sprintf("child-%d", i), "user", "2026-10-06T00:00:01.000Z", fmt.Sprintf("turn %d", i), nil))
	}
	writeSubagent(t, info, "long-child", map[string]any{"toolUseId": "long-run"}, rows...)
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-tool/long-run", "", 200)
	body := historyRecord(raw)
	var messages, activity []json.RawMessage
	_ = json.Unmarshal(body["messages"], &messages)
	_ = json.Unmarshal(body["activityMessages"], &activity)
	if len(messages) != 1000 || len(activity) != 1001 || string(body["truncated"]) != "true" || string(body["historyComplete"]) != "true" || stringField(historyRecord(messages[50]), "id") != "child-51" {
		t.Fatal("display truncation must preserve full Activity", len(messages), len(activity), string(body["historyComplete"]))
	}
	appendRows(t, info.Path, `{"broken":`, strings.Repeat("x", 8*1024*1024+1))
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-tool/absent-under-incomplete", "", 200)
	body = historyRecord(raw)
	if stringField(body, "status") != "unknown" || string(body["historyComplete"]) != "false" || string(body["truncated"]) != "true" {
		t.Fatal(string(raw))
	}
	// Actual metadata envelope, no preconstructed over-budget response.
	if err := os.WriteFile(filepath.Join(subagentDirectory(info), "agent-huge.meta.json"), []byte(strings.Repeat(" ", 64*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	raw = requestJSON(t, host, "GET", "/api/sessions/"+id+"/subagents/by-tool/long-run", "", 413)
	if !strings.Contains(string(raw), "SUBAGENT_METADATA_LIMIT") {
		t.Fatal(string(raw))
	}
}

func TestSubagentActivityRewriteDropsOnlyStrictPrefixFragmentAndMatchesToolResults(t *testing.T) {
	tool := func(id, name string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"id": id, "type": "tool_use", "timestamp": "2026-10-06T00:00:00.000Z", "content": []any{map[string]any{"type": "tool_use", "id": "Agent:0", "name": name, "input": map[string]any{}}}})
		return raw
	}
	first := tool("same-message", "Agent")
	result := json.RawMessage(`{"id":"done","type":"tool_result","timestamp":"2026-10-06T00:00:01.000Z","content":[{"type":"tool_result","tool_use_id":"Agent:0","content":"done"}]}`)
	fragments := []subagentFragment{{subagentTranscript{"old", []json.RawMessage{first}, []historyObject{}, true}, 1}, {subagentTranscript{"new", []json.RawMessage{first, result}, []historyObject{{"toolUseId": jsonString("Agent:0"), "status": jsonString("completed")}}, true}, 2}}
	activity := subagentActivityFragments(fragments)
	if len(activity.messages) != 2 || !strings.Contains(string(activity.messages[0]), "new/Agent:0") || !strings.Contains(string(activity.messages[1]), "new/Agent:0") || stringField(activity.notifications[0], "toolUseId") != "new/Agent:0" {
		t.Fatal(activity)
	}
	// Same message id with different work is not a superseded strict prefix.
	fragments[0].messages[0] = tool("same-message", "Bash")
	activity = subagentActivityFragments(fragments)
	if !strings.Contains(string(activity.messages[0]), "old/Agent:0") {
		t.Fatal("independent reused id was falsely treated as prefix", activity)
	}
}
