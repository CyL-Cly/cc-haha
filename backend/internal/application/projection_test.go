package application

import (
	"encoding/json"
	"testing"
)

func project(t *testing.T, p *Projection, text string) []json.RawMessage {
	t.Helper()
	out, err := p.Translate(json.RawMessage(text))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestProjectionDeduplicatesAPIIdentityAndRetainsLaterMessage(t *testing.T) {
	p := NewProjection()
	project(t, p, `{"type":"stream_event","event":{"type":"message_start","message":{"id":"first"}}}`)
	project(t, p, `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`)
	got := project(t, p, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"first"}}}`)
	if len(got) != 1 || obj(got[0]).String("text") != "first" {
		t.Fatal(string(raw(got)))
	}
	if got := project(t, p, `{"type":"assistant","message":{"id":"first","content":[{"type":"text","text":"first"}]}}`); len(got) != 0 {
		t.Fatal("buffered text duplicated", string(raw(got)))
	}
	got = project(t, p, `{"type":"assistant","message":{"id":"second","content":[{"type":"text","text":"second"}]}}`)
	if len(got) != 2 || obj(got[1]).String("text") != "second" {
		t.Fatal("unrelated nonstreamed message lost", string(raw(got)))
	}
}
func TestProjectionMalformedStreamToolInputUsesCompleteAssistant(t *testing.T) {
	p := NewProjection()
	project(t, p, `{"type":"stream_event","event":{"type":"message_start","message":{"id":"tool"}}}`)
	project(t, p, `{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call","name":"Write"}}}`)
	project(t, p, `{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}}`)
	if got := project(t, p, `{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`); len(got) != 0 {
		t.Fatal("premature tool complete", got)
	}
	got := project(t, p, `{"type":"assistant","message":{"id":"tool","content":[{"type":"tool_use","id":"call","name":"Write","input":{"file_path":"a","content":"b"}}]}}`)
	if len(got) != 1 || obj(got[0]).String("type") != "tool_use_complete" || obj(obj(got[0]).Get("input")).String("content") != "b" {
		t.Fatal(string(raw(got)))
	}
	if got := project(t, p, `{"type":"assistant","message":{"id":"tool","content":[{"type":"tool_use","id":"call","name":"Write","input":{"file_path":"a","content":"b"}}]}}`); len(got) != 0 {
		t.Fatal("duplicate tool complete", got)
	}
}
func TestProjectionInterleavedParentIDsAndAmbiguousRootResult(t *testing.T) {
	p := NewProjection()
	for _, parent := range []string{"one", "two"} {
		project(t, p, string(raw(map[string]any{"type": "assistant", "parent_tool_use_id": parent, "message": map[string]any{"id": parent, "content": []any{map[string]any{"type": "tool_use", "name": "Read", "id": "same", "input": map[string]any{"file_path": "a"}}}}})))
	}
	got := project(t, p, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"same","content":"done"}]}}`)
	if len(got) != 1 || obj(got[0]).String("toolUseId") != "same" || obj(got[0]).Get("parentToolUseId") != nil {
		t.Fatal("ambiguous scope guessed", string(raw(got)))
	}
}
func TestProjectionStopSuppressesAbortErrorAndRetainsUsage(t *testing.T) {
	p := NewProjection()
	p.Stopped = true
	if got := project(t, p, `{"type":"assistant","error":"cancelled","message":{"content":[]}}`); len(got) != 0 {
		t.Fatal(got)
	}
	got := project(t, p, `{"type":"result","is_error":true,"result":"cancelled","usage":{"input_tokens":7,"output_tokens":3},"duration_ms":50}`)
	if len(got) != 1 || obj(got[0]).String("type") != "message_complete" || numeric(obj(obj(got[0]).Get("usage")).Get("output_tokens")) != 3 {
		t.Fatal(string(raw(got)))
	}
}
