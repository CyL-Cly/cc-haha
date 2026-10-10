package server

import (
	"context"
	"fmt"
	"testing"
)

func TestSubagentLookupKeepsLatestUpdateForSameTaskAndPreservesOtherTaskAmbiguity(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("read opened Harness")
		return nil, nil
	})
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	info, e := s.findSession(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	appendRows(t, info.Path,
		subagentRow(t, "tool", "assistant", "2026-10-07T00:00:00.000Z", []any{map[string]any{"type": "tool_use", "id": "agent-tool", "name": "Agent", "input": map[string]any{"prompt": "task"}}}, nil),
		subagentRow(t, "result", "user", "2026-10-07T00:00:00.001Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "agent-tool", "content": "Async agent launched successfully.\nagentId: same-agent\nThe agent is working in the background."}}, map[string]any{"toolUseResult": map[string]any{"isAsync": true, "agentId": "same-agent"}}))
	writeSubagent(t, info, "same-agent", map[string]any{"toolUseId": "agent-tool"}, subagentRow(t, "child", "assistant", "2026-10-07T00:00:01.000Z", []any{map[string]any{"type": "text", "text": "done"}}, nil))
	notice := func(uuid, task, status, result string) string {
		return subagentRow(t, uuid, "user", "2026-10-07T00:00:02.000Z", fmt.Sprintf("<task-notification>\n<task-id>%s</task-id>\n<tool-use-id>agent-tool</tool-use-id>\n<status>%s</status>\n<result>%s</result>\n</task-notification>", task, status, result), nil)
	}
	appendRows(t, info.Path, notice("first", "same-agent", "completed", "FIRST_RESULT"), notice("second", "same-agent", "completed", "CONTINUED_RESULT"))
	response, e := s.subagentRunByTool(context.Background(), info, "agent-tool", "")
	if e != nil || response["status"] != "completed" || response["result"] != "CONTINUED_RESULT" || response["canSendMessage"] != false {
		t.Fatalf("same task updates result%v error%v", response, e)
	}
	appendRows(t, info.Path, notice("different", "another-agent", "failed", "DIFFERENT_TASK"))
	response, e = s.subagentRunByTool(context.Background(), info, "agent-tool", "")
	if e != nil || response["status"] != "running" {
		t.Fatalf("different task ambiguity was discarded result%v error%v", response, e)
	}
}
