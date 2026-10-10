package application

import (
	"encoding/json"
	"testing"
)

func routeAgent(t *testing.T, p *Projection, stream, kind string, message any, failure any) []json.RawMessage {
	t.Helper()
	return project(t, p, string(raw(map[string]any{"type": "system", "subtype": "agent_run_message", "run_agent_id": " actual-run ", "stream_id": stream, "target_agent_id": " child ", "target_agent_scope_id": " scope ", "event_kind": kind, "message": message, "error": failure})))
}

func TestAgentProjectionKeepsSourceStreamStateSeparateFromMainAndOtherChildren(t *testing.T) {
	p := NewProjection()
	start := map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"id": "shared-api-id"}}}
	assistant := map[string]any{"type": "assistant", "message": map[string]any{"id": "shared-api-id", "content": []any{map[string]any{"type": "text", "text": "CHILD OR MAIN TEXT"}}}}
	for _, stream := range []string{"stream-one", "stream-two"} {
		frames := routeAgent(t, p, stream, "message", start, nil)
		if len(frames) != 1 || obj(frames[0]).String("type") != "agent_run_event" || obj(frames[0]).String("runAgentId") != "actual-run" || obj(frames[0]).String("targetAgentScopeId") != "scope" {
			t.Fatalf("Source child route %s", raw(frames))
		}
	}
	if frames := project(t, p, string(raw(assistant))); len(frames) != 2 || obj(frames[1]).String("text") != "CHILD OR MAIN TEXT" {
		t.Fatalf("child consumed main API identity: %s", raw(frames))
	}
	project(t, p, `{"type":"result","usage":{}}`)
	if frames := routeAgent(t, p, "stream-one", "message", assistant, nil); len(frames) != 0 {
		t.Fatalf("main completion reset child identity: %s", raw(frames))
	}
	if frames := routeAgent(t, p, "stream-one", "complete", nil, nil); len(frames) != 1 || obj(obj(frames[0]).Get("event")).String("state") != "idle" {
		t.Fatalf("child completion %s", raw(frames))
	}
	if frames := routeAgent(t, p, "stream-two", "message", assistant, nil); len(frames) != 0 {
		t.Fatalf("completion reset another child: %s", raw(frames))
	}
	if frames := routeAgent(t, p, "stream-one", "message", assistant, nil); len(frames) != 2 || obj(obj(frames[1]).Get("event")).String("text") != "CHILD OR MAIN TEXT" {
		t.Fatalf("terminal route retained stale child state: %s", raw(frames))
	}
}

func TestAgentProjectionMatchesSourceTerminalAndMalformedMessageBehavior(t *testing.T) {
	p := NewProjection()
	for _, value := range []any{nil, false, true, 0, 1, "", "text", []any{1}} {
		if frames := routeAgent(t, p, "stream", "message", value, nil); len(frames) != 0 {
			t.Fatalf("non-envelope produced frames: %s", raw(frames))
		}
	}
	frames := routeAgent(t, p, "stream", "cancelled", nil, nil)
	if len(frames) != 2 || obj(obj(frames[0]).Get("event")).String("cause") != "stream_retry" || obj(obj(frames[1]).Get("event")).String("state") != "idle" {
		t.Fatalf("Source cancellation %s", raw(frames))
	}
	for _, tc := range []struct {
		input any
		want  string
	}{{nil, "Agent run failed"}, {false, "Agent run failed"}, {19, "Agent run failed"}, {"", ""}, {"actual error", "actual error"}} {
		frames = routeAgent(t, p, "stream", "error", nil, tc.input)
		if len(frames) != 1 || obj(obj(frames[0]).Get("event")).String("message") != tc.want || obj(obj(frames[0]).Get("event")).String("code") != "AGENT_RUN_ERROR" {
			t.Fatalf("Source error fallback %s", raw(frames))
		}
	}
}
