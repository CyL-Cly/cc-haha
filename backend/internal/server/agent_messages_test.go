package server

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http/httptest"
	"strings"
	"testing"
)

type messageTransport struct {
	transportSession
	id, content string
	calls       int
}

func (m *messageTransport) SendAgentMessage(_ context.Context, id, content string) (protocol.JSONObject, error) {
	m.id = id
	m.content = content
	m.calls++
	return protocol.NewJSONObject(protocol.JSONField{Name: "agent_id", Value: json.RawMessage(`"child-one"`)}, protocol.JSONField{Name: "delivery", Value: json.RawMessage(`"queued"`)}), nil
}
func TestSubagentMessageAPIValidatesThenCallsExistingLiveTaskFacade(t *testing.T) {
	transport := &messageTransport{}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return transport, nil })
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	info, e := s.findSession(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	appendRows(t, info.Path, subagentRow(t, "parent-tool", "assistant", "2026-10-07T00:00:00.000Z", []any{map[string]any{"type": "tool_use", "id": "agent-call", "name": "Agent", "input": map[string]any{"prompt": "task"}}}, nil))
	writeSubagent(t, info, "child-one", map[string]any{"toolUseId": "agent-call"}, subagentRow(t, "child-user", "user", "2026-10-07T00:00:01.000Z", "actual child task", nil))
	path := "/api/sessions/" + id + "/subagents/by-tool/agent-call/messages"
	for _, body := range []string{`not json`, `{}`, `[]`, `{"content":1}`, `{"content":"\ufeff \t"}`} {
		requestJSON(t, host, "POST", path, body, 400)
	}
	if transport.calls != 0 {
		t.Fatal("invalid request reached actual consumer")
	}
	data := requestJSON(t, host, "POST", path, `{"content":"\ufeff  actual followup  \n","unknown":"keep out"}`, 200)
	if transport.calls != 1 || transport.id != "child-one" || transport.content != "actual followup" {
		t.Fatal(transport.calls, transport.id, transport.content)
	}
	var response map[string]any
	_ = json.Unmarshal(data, &response)
	if response["ok"] != true || response["agent_id"] != "child-one" || response["delivery"] != "queued" {
		t.Fatal(string(data))
	}
	// A missing resolved id is a conflict before the actual Harness is opened.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://127.0.0.1", strings.NewReader(`{"content":"followup"}`))
	e = s.sendSubagentMessage(recorder, request, id, "not-resumable", map[string]any{})
	typed, ok := e.(*apiError)
	if !ok || typed.status != 409 || transport.calls != 1 {
		t.Fatal(e)
	}
}
