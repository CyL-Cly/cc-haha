package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func historyFixture(values ...string) []sessionstore.Entry {
	entries := []sessionstore.Entry{}
	for _, value := range values {
		entries = append(entries, sessionstore.Entry{Entry: json.RawMessage(value)})
	}
	return entries
}

func TestHistoryProjectionMatchesToolStopUsageAndNotificationContracts(t *testing.T) {
	entries := historyFixture(
		`{"type":"assistant","uuid":"call","timestamp":"2026-10-05T00:00:00.000Z","requestId":"request","message":{"role":"assistant","id":"provider-message","model":"fixture","content":[{"type":"tool_use","id":"write","name":"Write","input":{"file_path":"fixture"}}],"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":"invalid","cache_creation_input_tokens":0,"future":9}}}`,
		`{"type":"user","uuid":"result","parentUuid":"call","timestamp":"2026-10-05T00:00:01.000Z","toolUseResult":{"future":{"keep":true}},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"write","content":"saved"}]}}`,
		`{"type":"user","uuid":"meta","isMeta":true,"message":{"role":"user","content":"internal"}}`,
		`{"type":"assistant","uuid":"no-response","message":{"role":"assistant","content":"No response requested."}}`,
		`{"type":"user","uuid":"stopped","timestamp":"2026-10-05T00:00:02.000Z","parentUuid":"result","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"user","uuid":"notification","timestamp":"2026-10-05T00:00:03.000Z","message":{"role":"user","content":"<task-notification><tool-use-id>shared</tool-use-id><status>killed</status><summary>Stopped &amp; saved</summary></task-notification>"}}`,
		`{"type":"cc-haha-task-notification","timestamp":"2026-10-05T00:00:04.000Z","taskNotification":{"toolUseId":"shared","ownerAgentId":"child","taskId":"task-child","status":"completed","result":"first","ignored":"not projected"}}`,
		`{"type":"cc-haha-task-notification","timestamp":"2026-10-05T00:00:05.000Z","taskNotification":{"toolUseId":"shared","ownerAgentId":"child","status":"failed","result":"latest"}}`,
		`{"type":"assistant","uuid":"inherited","forkedFrom":{"sessionId":"source","messageUuid":"original"},"message":{"role":"assistant","id":"fork-provider","content":"fork text","usage":{"input_tokens":100}}}`,
	)
	messages, notifications := projectHistory(entries)
	if len(messages) != 4 || len(notifications) != 2 {
		t.Fatalf("messages=%s notifications=%s", messages, notifications)
	}
	call, result, stopped, inherited := historyRecord(messages[0]), historyRecord(messages[1]), historyRecord(messages[2]), historyRecord(messages[3])
	if stringField(call, "type") != "tool_use" || stringField(result, "type") != "tool_result" || stringField(call, "usageKey") != "provider-message\x00request" || string(historyRecord(call["usage"])["input_tokens"]) != "3" || len(historyRecord(call["usage"])["cache_read_input_tokens"]) != 0 || len(historyRecord(call["usage"])["future"]) != 0 {
		t.Fatal("tool/usage projection changed")
	}
	if string(result["toolUseResult"]) != `{"future":{"keep":true}}` || stringField(stopped, "type") != "system" || stringField(historyRecord(stopped["content"]), "subtype") != "generation_stopped" || len(stopped["parentUuid"]) != 0 || len(inherited["usage"]) != 0 || len(inherited["usageKey"]) != 0 {
		t.Fatal("tool result/stop/fork usage changed")
	}
	if stringField(notifications[0], "status") != "stopped" || stringField(notifications[0], "summary") != "Stopped & saved" || stringField(notifications[1], "result") != "latest" || stringField(notifications[1], "taskId") != "shared" || len(notifications[1]["ignored"]) != 0 {
		t.Fatal("notification ownership, replacement or XML decode changed")
	}
}

func TestHistoryProjectionRestoresReferenceCollaborationGoalAndChildOwner(t *testing.T) {
	reference := "read it\n\n<session_references>\ntransport instruction\n[{\"sessionId\":\"source\",\"title\":\"ignored\"}]\n</session_references>"
	envelope, _ := json.Marshal(map[string]string{"senderSessionId": "sender", "messageId": "delivery", "text": "payload"})
	ref, _ := json.Marshal(reference)
	collaboration, _ := json.Marshal(collaborationPrefix + string(envelope))
	entries := historyFixture(
		`{"type":"user","uuid":"ref","bodyTruncated":true,"cwd":"/fixture","message":{"role":"user","content":`+string(ref)+`}}`,
		`{"type":"user","uuid":"collab","isMeta":true,"message":{"role":"user","content":`+string(collaboration)+`}}`,
		`{"type":"system","uuid":"goal","subtype":"local_command","content":"<command-name>/goal</command-name><local-command-stdout>Goal set: finish</local-command-stdout>"}`,
		`{"type":"assistant","uuid":"parent-agent","message":{"role":"assistant","content":[{"type":"tool_use","name":"Agent","id":"child-owner","input":{}}]}}`,
		`{"type":"user","uuid":"child-first","isSidechain":true,"parentUuid":"parent-agent","message":{"role":"user","content":"child prompt"}}`,
		`{"type":"assistant","uuid":"child-next","isSidechain":true,"parentUuid":"child-first","message":{"role":"assistant","content":"child answer"}}`,
	)
	messages, _ := projectHistory(entries)
	if len(messages) != 6 {
		t.Fatal("visible history lost")
	}
	first, second, last := historyRecord(messages[0]), historyRecord(messages[1]), historyRecord(messages[5])
	if stringField(first, "content") != "read it" || string(first["sessionReferences"]) != `[{"sessionId":"source"}]` || string(first["bodyTruncated"]) != "true" || stringField(first, "cwd") != "/fixture" || stringField(second, "content") != "payload" || stringField(historyRecord(second["collaboration"]), "sourceSessionId") != "sender" || stringField(last, "parentToolUseId") != "child-owner" || stringField(historyRecord(messages[2]), "type") != "system" {
		t.Fatal("reference/collaboration/goal/ancestry projection changed")
	}
	for _, content := range []string{shutdownTeamPrompt, "<command-name>/unknown</command-name>Mixed transport body"} {
		raw, _ := json.Marshal(content)
		if _, ok := messageEntry(json.RawMessage(`{"type":"user","message":{"role":"user","content":` + string(raw) + `}}`)); ok {
			t.Fatal("hidden transport message leaked")
		}
	}
	if _, ok := messageEntry(json.RawMessage(`{"type":"user","message":{"role":"user","content":"<command-name>/help</command-name><command-args>topic</command-args>"}}`)); !ok {
		t.Fatal("valid visible command metadata was hidden")
	}
}

func TestHistoryMessagesHTTPReturnsActualPersistedToolTimeline(t *testing.T) {
	options := testOptions(t, func(_ context.Context, _ SessionOptions, _ Emit) (LiveSession, error) {
		return &transportSession{}, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(info.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(strings.Join([]string{`{"type":"assistant","uuid":"persisted-tool","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}`, `{"type":"user","uuid":"persisted-result","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool","content":"real persisted result"}]}}`}, "\n") + "\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages?mode=full", "", 200)
	var response struct{ Messages []historyObject }
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 2 || stringField(response.Messages[0], "type") != "tool_use" || stringField(response.Messages[1], "type") != "tool_result" {
		t.Fatal("HTTP history changed the saved tool timeline")
	}
}
