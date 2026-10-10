package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeTranscriptTrimPreservesOriginalLinesMetadataAndPersistencePolicy(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	persist := true
	options.PersistSession = func(context.Context) (bool, error) { return persist, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	first := subagentRow(t, "first-user", "user", "2026-10-06T00:00:00.000Z", "keep prompt", nil)
	answer := subagentRow(t, "first-answer", "assistant", "2026-10-06T00:00:01.000Z", "keep reply", nil)
	second := subagentRow(t, "second-user", "user", "2026-10-06T00:00:02.000Z", "remove prompt", nil)
	last := subagentRow(t, "second-answer", "assistant", "2026-10-06T00:00:03.000Z", "remove reply", nil)
	hidden := subagentRow(t, "hidden-meta-user", "user", "2026-10-06T00:00:04.000Z", "internal prompt", map[string]any{"isMeta": true})
	meta := ` { "type": "session-meta", "uuid": "metadata-uuid", "future": { "keep": 9007199254740993 } } `
	noID := `{"type":"user","message":{"role":"user","content":"no uuid survives Source copy"}}`
	original := first + "\r\n" + answer + "\r" + second + "\n" + last + "\n" + hidden + "\n" + meta + "\n\n" + `{"broken":` + "\n" + noID
	if err := os.WriteFile(info.Path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	persist = false
	result, err := server.trimSessionMessagesFrom(context.Background(), id, "second-user")
	if err != nil || result.RemovedCount != 0 || len(result.RemovedMessageIDs) != 0 {
		t.Fatal(result, err)
	}
	unchanged, _ := os.ReadFile(info.Path)
	if string(unchanged) != original {
		t.Fatal("retention-zero native policy changed transcript")
	}
	persist = true
	result, err = server.trimSessionMessagesFrom(context.Background(), id, "second-user")
	if err != nil {
		t.Fatal(err)
	}
	// A UUID-less visible row contributes a generated removed id but its original
	// line survives, exactly as Source's native kept predicate specifies.
	if result.RemovedCount != 3 || result.RemovedMessageIDs[0] != "second-user" || result.RemovedMessageIDs[1] != "second-answer" {
		t.Fatal(result)
	}
	actual, _ := os.ReadFile(info.Path)
	expected := first + "\n" + answer + "\n" + meta + "\n\n" + `{"broken":` + "\n" + noID + "\n"
	if string(actual) != expected {
		t.Fatal("original line projection mismatch", string(actual), expected)
	}
	stat, _ := os.Stat(info.Path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal(stat.Mode())
	}
	leftovers, _ := filepath.Glob(info.Path + ".rewind-*.tmp")
	if len(leftovers) != 0 {
		t.Fatal("native temporary files leaked", leftovers)
	}
	_, err = server.trimSessionMessagesFrom(context.Background(), id, "absent")
	if err == nil || !strings.Contains(err.Error(), "Message not found in active session chain") {
		t.Fatal(err)
	}
}

func TestCheckpointEvidenceIncludesCanonicalNativeChildToolsAndUnfinishedLinksRemainIncomplete(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tool := map[string]any{"type": "tool_use", "id": "root-agent", "name": "Agent", "input": map[string]any{"description": "real stored child"}}
	appendRows(t, info.Path, subagentRow(t, "root-user", "user", "2026-10-06T00:00:00.000Z", "parent prompt", nil), subagentRow(t, "root-agent-tool", "assistant", "2026-10-06T00:00:01.000Z", []any{tool}, nil))
	evidence, err := server.checkpointMessages(context.Background(), id)
	if err != nil || evidence.Complete {
		t.Fatal("unfinished child cannot establish complete checkpoint evidence", evidence, err)
	}
	writeSubagent(t, info, "stored-child", map[string]any{"toolUseId": "root-agent"},
		subagentRow(t, "child-user", "user", "2026-10-06T00:00:02.000Z", "child prompt", map[string]any{"isSidechain": true}),
		subagentRow(t, "child-write", "assistant", "2026-10-06T00:00:03.000Z", []any{map[string]any{"type": "tool_use", "id": "write-one", "name": "Write", "input": map[string]any{"file_path": "child.txt", "content": "bytes"}}}, map[string]any{"isSidechain": true}),
		subagentRow(t, "child-result", "user", "2026-10-06T00:00:04.000Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "write-one", "content": "native result"}}, map[string]any{"isSidechain": true}),
		subagentRow(t, "child-answer", "assistant", "2026-10-06T00:00:05.000Z", "child complete", nil))
	appendRows(t, info.Path, subagentRow(t, "parent-result", "user", "2026-10-06T00:00:06.000Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "root-agent", "content": []any{map[string]any{"type": "text", "text": "agentId: stored-child"}}}}, nil))
	evidence, err = server.checkpointMessages(context.Background(), id)
	if err != nil || !evidence.Complete || len(evidence.Messages) != 5 {
		t.Fatal(evidence, err)
	}
	child, _ := evidence.Messages[3].MarshalJSON()
	if !strings.Contains(string(child), `root-agent/stored-child/write-one`) || !strings.Contains(string(child), `"original_tool_use_id":"write-one"`) || !strings.Contains(string(child), `"parentToolUseId":"root-agent"`) {
		t.Fatal(string(child))
	}
	// Source's workspace API consumes root recovery while checkpoints separately
	// include child tools. Do not silently mix these two evidence contracts.
	status := requestJSON(t, host, "GET", "/api/sessions/"+id+"/workspace/status", "", 200)
	if string(historyRecord(status)["changedFiles"]) != "[]" {
		t.Fatal("workspace status included checkpoint-only child changes", string(status))
	}
	diff := requestJSON(t, host, "GET", "/api/sessions/"+id+"/workspace/diff?path=child.txt", "", 200)
	if stringField(historyRecord(diff), "state") != "not_git_repo" {
		t.Fatal("workspace diff included checkpoint-only child changes", string(diff))
	}
	if err := os.Remove(subagentPath(info, "stored-child")); err != nil {
		t.Fatal(err)
	}
	evidence, err = server.checkpointMessages(context.Background(), id)
	if err != nil || evidence.Complete {
		t.Fatal("missing child transcript cannot establish complete evidence", evidence, err)
	}
}

type stopBarrierSession struct {
	started chan struct{}
	release chan struct{}
	closes  atomic.Int32
}

func (*stopBarrierSession) Handle(context.Context, json.RawMessage) error       { return nil }
func (*stopBarrierSession) Snapshot(context.Context) ([]json.RawMessage, error) { return nil, nil }
func (session *stopBarrierSession) Close(ctx context.Context) error {
	if session.closes.Add(1) != 1 {
		return nil
	}
	close(session.started)
	select {
	case <-session.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestConcurrentStopSeamWaitsForFirstSharedRuntimeCloseAndReopensStoredHistory(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path, subagentRow(t, "kept-user", "user", "2026-10-06T00:00:00.000Z", "stored prompt", nil))
	live := &stopBarrierSession{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(server.ctx)
	defer cancel()
	runtime := &runtimeSession{ctx: ctx, cancel: cancel, live: live}
	server.mu.Lock()
	server.runtimes[id] = runtime
	server.mu.Unlock()
	finished := make(chan error, 2)
	go func() { finished <- server.stopSessionAndWait(context.Background(), id) }()
	<-live.started
	go func() { finished <- server.stopSessionAndWait(context.Background(), id) }()
	select {
	case err := <-finished:
		t.Fatal("concurrent stop returned before native close drain", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(live.release)
	for i := 0; i < 2; i++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	if live.closes.Load() != 1 {
		t.Fatal(live.closes.Load())
	}
	reopened, err := server.runtime(id)
	if err != nil || reopened == runtime || !reopened.options.Resume || reopened.options.ID != id {
		t.Fatal(reopened, err)
	}
}

func TestPhysicalTrimRequiresActualPolicyBinding(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	_, err := server.trimSessionMessagesFrom(context.Background(), id, "user")
	var expected *apiError
	if !errors.As(err, &expected) || expected.status != 501 || !strings.Contains(err.Error(), "persistence policy") {
		t.Fatal(err, expected)
	}
}
