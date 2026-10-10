package localindex

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func reductionOptions() ReductionOptions {
	parser := activitystatsapi.NewIndexDateParser(time.UTC)
	compare := collate.New(language.English)
	return ReductionOptions{Platform: "darwin", Location: time.UTC, ParseDate: parser.Parse, Compare: compare.CompareString}
}
func projectionSeed() *TranscriptProjection {
	return &TranscriptProjection{Summary: protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: raw("seed")}, protocol.JSONField{Name: "createdAt", Value: raw("2026-10-01T00:00:00Z")}, protocol.JSONField{Name: "modifiedAt", Value: raw("2026-10-06T00:00:00Z")}, protocol.JSONField{Name: "messageCount", Value: raw(0)}, protocol.JSONField{Name: "workDir", Value: raw("/fallback")})}
}
func reductionChunks(entries []any, start int64) ([]TranscriptChunk, int64) {
	out := []TranscriptChunk{}
	for _, entry := range entries {
		text := string(raw(entry)) + "\n"
		length := int64(len(text))
		out = append(out, TranscriptChunk{Text: text, ByteStart: start, ByteLength: &length, CompleteLine: true})
		start += length
	}
	return out, start
}
func TestTranscriptReducerActualSourceStateTransitions(t *testing.T) {
	user := func(stamp string, content any) any {
		return map[string]any{"type": "user", "uuid": "user-id", "timestamp": stamp, "message": map[string]any{"role": "user", "content": content}}
	}
	assistant := func(id, request, stamp, model string, usage, content any) any {
		return map[string]any{"type": "assistant", "uuid": id, "requestId": request, "timestamp": stamp, "version": "2.1.3", "sessionId": "session", "message": map[string]any{"id": id, "role": "assistant", "model": model, "usage": usage, "content": content}}
	}
	usage := map[string]any{"input_tokens": 1000, "output_tokens": 50, "cache_read_input_tokens": 700, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": 4, "ephemeral_1h_input_tokens": 3}, "server_tool_use": map[string]any{"web_search_requests": 1}, "iterations": []any{map[string]any{"type": "advisor_message", "model": "claude-opus-5-5", "input_tokens": 111, "output_tokens": 12}, map[string]any{"type": "other", "model": "claude-opus-5-5", "input_tokens": 999}}}
	tools := []any{map[string]any{"type": "tool_use", "name": "Skill", "input": map[string]any{"skill": " owned-skill "}}, map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "echo 3-shotted by fixture"}}, nil, map[string]any{"type": "text", "text": "done"}}
	entries := []any{map[string]any{"type": "session-meta", "workDir": "/workspace", "permissionMode": "plan", "runtimeProviderId": "provider", "runtimeModelId": "deepseek-flash", "effortLevel": "high"}, user("2026-10-06T00:00:00Z", "你好 first task"), assistant("a1", "r1", "2026-10-06T00:01:00Z", "claude-sonnet-5", usage, tools), assistant("a1", "r1", "2026-10-06T00:01:00Z", "claude-sonnet-5", usage, []any{map[string]any{"type": "text", "text": "duplicate content block"}}), map[string]any{"type": "speculation-accept", "timeSavedMs": 125}, map[string]any{"type": "custom-title", "customTitle": 123}, map[string]any{"type": "custom-title", "customTitle": ""}, map[string]any{"type": "session-meta", "runtimeProviderId": nil, "runtimeModelId": "deepseek-flash"}, map[string]any{"type": "worktree-state", "worktreeSession": map[string]any{"worktreePath": "/owned-worktree", "worktreeName": "fixture", "sessionId": "session"}}, map[string]any{"type": "metadata", "repository": map[string]any{"repoRoot": "/repo", "requestedWorkDir": "/repo", "branch": "fixture", "worktree": false, "baseRef": "main"}}, map[string]any{"type": "assistant", "isSidechain": true, "timestamp": "2026-10-06T09:00:00Z", "message": map[string]any{"role": "assistant", "content": "side", "usage": usage, "model": "claude-opus-5-5"}}, map[string]any{"type": "system", "timestamp": "2026-10-06T00:02:00Z", "content": "system"}}
	chunks, end := reductionChunks(entries, 0)
	appendChunks, end := reductionChunks([]any{user("2026-10-07T00:00:00Z", "resumed"), assistant("a2", "r2", "2026-10-07T00:03:00Z", "claude-opus-5-5", map[string]any{"input_tokens": 123, "output_tokens": 5, "speed": "fast"}, []any{map[string]any{"type": "text", "text": "next"}}), map[string]any{"type": "worktree-state", "worktreeSession": nil}, map[string]any{"type": "session-meta", "runtimeProviderId": "p", "runtimeModelId": "m", "effortLevel": "low"}, map[string]any{"type": "session-meta", "effortLevel": "wrong"}}, end)
	incomplete := TranscriptChunk{Text: `{"type":"user"`, ByteStart: end, CompleteLine: false}
	finish, end := reductionChunks([]any{user("2026-10-07T00:04:00Z", "tail completed")}, end)
	_ = end
	ops := []map[string]any{{"chunks": chunks}, {"chunks": appendChunks}, {"chunks": []TranscriptChunk{incomplete}}, {"chunks": finish}, {"chunks": []TranscriptChunk{}}, {"chunks": []TranscriptChunk{{Text: "{}\n", ByteStart: 999, CompleteLine: true}}}, {"chunks": []TranscriptChunk{{Text: "bad JSON\n", ByteStart: 0, CompleteLine: true}}, "seed": projectionSeed()}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "reduce", "seed": projectionSeed(), "operations": ops})
	var rows []json.RawMessage
	_ = json.Unmarshal(source["observations"], &rows)
	seed := projectionSeed()
	for n, row := range ops {
		if replacement, ok := row["seed"].(*TranscriptProjection); ok {
			seed = replacement
		}
		value := row["chunks"].([]TranscriptChunk)
		result, locators, e := ReduceTranscript(value, seed, reductionOptions())
		observation := map[string]any{"result": map[string]any{"projection": result, "locators": locators}}
		if e != nil {
			code := any(nil)
			if c, ok := e.(*Error); ok {
				code = c.Code
			}
			observation = map[string]any{"error": e.Error(), "code": code}
		} else {
			seed = result
		}
		if !reflect.DeepEqual(decoded(observation), decoded(rows[n])) {
			t.Fatalf("Reduction Source %d native=%s\nSource=%s", n, raw(observation), rows[n])
		}
	}
}
func TestTranscriptReducerActualSourceFirstLastValidityAndMetadata(t *testing.T) {
	cases := [][]any{{map[string]any{"type": "custom-title", "customTitle": []any{"a", "b"}}, map[string]any{"type": "metadata", "repository": []any{"array"}}}, {map[string]any{"type": "user", "timestamp": "invalid date", "message": map[string]any{"role": "user", "content": "valid text"}}, map[string]any{"type": "assistant", "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"role": "assistant", "usage": map[string]any{"input_tokens": 1}, "model": "deepseek-flash"}}}, {map[string]any{"type": "user", "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"role": "user", "content": "first"}}, map[string]any{"type": "assistant", "timestamp": nil, "message": map[string]any{"role": "assistant", "content": "no final timestamp"}}}, {map[string]any{"type": "metadata", "entrypoint": "claude-desktop-team-worker", "messageId": "legacy", "parent_tool_use_id": "parent"}}, {map[string]any{"type": "system", "subtype": "local_command", "content": "<command-name>goal</command-name><command-args>write it</command-args>"}, map[string]any{"type": "ai-title", "aiTitle": "<tag>hidden</tag> AI title"}}, {map[string]any{"type": "assistant", "timestamp": "2026-10-06T00:00:00Z", "isSidechain": "truthy but not boolean", "message": map[string]any{"role": "assistant", "model": "deepseek-flash", "usage": map[string]any{"input_tokens": 12, "output_tokens": 2}}}}, {nil}, {42, "primitive", []any{}}, {map[string]any{"type": "user", "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"role": "user", "content": []any{nil, map[string]any{"type": "text", "text": "text"}}}}}}
	ops := []map[string]any{}
	for _, entries := range cases {
		chunks, _ := reductionChunks(entries, 0)
		ops = append(ops, map[string]any{"chunks": chunks, "seed": projectionSeed()})
	}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "reduce", "operations": ops})
	var rows []json.RawMessage
	_ = json.Unmarshal(source["observations"], &rows)
	for n, row := range ops {
		result, locators, e := ReduceTranscript(row["chunks"].([]TranscriptChunk), projectionSeed(), reductionOptions())
		observation := map[string]any{"result": map[string]any{"projection": result, "locators": locators}}
		if e != nil {
			observation = map[string]any{"error": e.Error(), "code": nil}
		}
		if !reflect.DeepEqual(decoded(observation), decoded(rows[n])) {
			t.Fatalf("Reduction edge %d native=%s Source=%s", n, raw(observation), rows[n])
		}
	}
}
