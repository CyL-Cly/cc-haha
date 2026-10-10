package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func TestTurnCheckpointHTTPUsesOriginalTranscriptSnapshotsAndNativeBackups(t *testing.T) {
	opens := 0
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		opens++
		return &transportSession{}, nil
	})
	// No Bash records occur in this boundary fixture. Application integration
	// supplies the actual Source shell classifier; this fixture never runs Agent.
	options.RecordedCommandIsReadOnly = func(string) bool { return false }
	options.PersistSession = func(context.Context) (bool, error) { return true, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(options.WorkDir, "actual.txt")
	if err := os.WriteFile(target, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backupDirectory := filepath.Join(options.ConfigDir, "file-history", id)
	if err := os.MkdirAll(backupDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDirectory, "after-backup"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"type": "file-history-snapshot", "snapshot": map[string]any{"messageId": "user-one", "trackedFileBackups": map[string]any{target: map[string]any{"backupFileName": nil}}, "completedFileBackups": map[string]any{target: map[string]any{"backupFileName": "after-backup"}}, "futureSnapshot": true}})
	user, _ := json.Marshal(map[string]any{"type": "user", "uuid": "user-one", "cwd": options.WorkDir, "message": map[string]any{"role": "user", "content": "create actual.txt"}})
	tool, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "tool-one", "parentUuid": "user-one", "cwd": options.WorkDir, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "write-one", "name": "Write", "input": map[string]any{"file_path": target, "content": "after\n"}}}}})
	result, _ := json.Marshal(map[string]any{"type": "user", "uuid": "result-one", "parentUuid": "tool-one", "cwd": options.WorkDir, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "write-one", "content": "created"}}}})
	done, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "done-one", "parentUuid": "result-one", "cwd": options.WorkDir, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}}})
	appendRows(t, info.Path, string(snapshot), string(user), string(tool), string(result), string(done))
	for _, suffix := range []string{"", "?frozen=true"} {
		raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints"+suffix, "", 200)
		var body struct {
			Checkpoints []sessionrewind.TurnCheckpoint
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Checkpoints) != 1 || body.Checkpoints[0].Target.TargetUserMessageID != "user-one" || !body.Checkpoints[0].Code.Available || !body.Checkpoints[0].RestoreAvailable || len(body.Checkpoints[0].Code.FilesChanged) != 1 || body.Checkpoints[0].Code.FilesChanged[0] != target || body.Checkpoints[0].Code.Insertions != 1 || body.Checkpoints[0].Code.Deletions != 0 {
			t.Fatal(string(raw))
		}
	}
	evidence, err := server.checkpointSnapshots(context.Background(), id)
	if err != nil || !evidence.Complete || len(evidence.Rows) != 2 || !strings.Contains(string(evidence.Rows[1]), "futureSnapshot") {
		t.Fatal("snapshot backing lost original unknown fields", evidence, err)
	}
	requestJSON(t, host, "POST", "/api/sessions/"+id+"/turn-checkpoints", "{}", 405)
	diff := requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints/diff?targetUserMessageId=user-one&path="+url.QueryEscape(target), "", 200)
	if stringField(historyRecord(diff), "state") != "ok" || !strings.Contains(stringField(historyRecord(diff), "diff"), "+after") {
		t.Fatal(string(diff))
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints/diff?path="+url.QueryEscape(target), "", 400)
	if opens != 0 {
		t.Fatal("checkpoint inspection opened a second Agent", opens)
	}
	for _, body := range []string{`{"targetUserMessageId":"user-one","dryRun":true}`, `{"userMessageIndex":0,"dryRun":{},"expectedContent":"create actual.txt"}`} {
		preview := requestJSON(t, host, "POST", "/api/sessions/"+id+"/rewind", body, 200)
		if stringField(historyRecord(historyRecord(preview)["target"]), "targetUserMessageId") != "user-one" || !strings.Contains(string(preview), `"restoreAvailable":true`) {
			t.Fatal(string(preview))
		}
		if bytes, err := os.ReadFile(target); err != nil || string(bytes) != "after\n" {
			t.Fatal("dry run changed real workspace", string(bytes), err)
		}
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/rewind", "", 405)
	for _, body := range []string{"{", `{}`, `{"userMessageIndex":0.5}`, `{"userMessageIndex":"0"}`, `{"userMessageIndex":0,"mode":"code","dryRun":true}`, `{"userMessageIndex":0,"expectedContent":"stale","dryRun":true}`} {
		requestJSON(t, host, "POST", "/api/sessions/"+id+"/rewind", body, 400)
	}
	rewound := requestJSON(t, host, "POST", "/api/sessions/"+id+"/rewind", `{"targetUserMessageId":"user-one","expectedContent":"create actual.txt"}`, 200)
	if !strings.Contains(string(rewound), `"messagesRemoved":4`) || !strings.Contains(string(rewound), `"mode":"both"`) {
		t.Fatal(string(rewound))
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("real workspace file was not rewound", err)
	}
	messages := requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages", "", 200)
	if !strings.Contains(string(messages), `"messages":[]`) {
		t.Fatal("physical transcript still contains removed turn", string(messages))
	}
	if opens != 0 {
		t.Fatal("HTTP rewind created another Harness", opens)
	}
}

func TestTurnCheckpointBudgetsRejectRealOversizedTranscriptAndSnapshotCatalog(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.RecordedCommandIsReadOnly = func(string) bool { return false }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(info.Path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(16*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 413)
	if !strings.Contains(string(raw), "HISTORY_CHECKPOINT_PREVIEW_LIMIT") {
		t.Fatal(string(raw))
	}
	id = createTestSession(t, host, options.WorkDir)
	info, err = server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 1000)
	for index := range rows {
		rows[index] = `{"type":"file-history-snapshot","snapshot":{"messageId":"same","trackedFileBackups":{}}}`
	}
	appendRows(t, info.Path, rows...)
	_, err = server.checkpointSnapshots(context.Background(), id)
	var api *apiError
	if !errors.As(err, &api) || api.status != 413 || api.code != "HISTORY_WORKSPACE_LIMIT" {
		t.Fatal("Source snapshot count budget not applied", err)
	}
}

func TestTurnCheckpointUsesFallibleClassifierAndDoesNotHideUnsupportedCommand(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	calls := 0
	options.ClassifyRecordedCommand = func(command string) (bool, error) {
		calls++
		if command != "unported-command" {
			t.Fatalf("classifier received %q", command)
		}
		return false, &sessionrewind.UnsupportedRecordedCommand{Feature: "fixture unsupported shell shape"}
	}
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, info.Path,
		subagentRow(t, "command-user", "user", "2026-10-06T00:00:00.000Z", "run command", nil),
		subagentRow(t, "command-tool", "assistant", "2026-10-06T00:00:01.000Z", []any{map[string]any{"type": "tool_use", "id": "bash-call", "name": "Bash", "input": map[string]any{"command": "unported-command"}}}, nil))
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 501)
	if calls != 1 || !strings.Contains(string(raw), "fixture unsupported shell shape") {
		t.Fatal(calls, string(raw))
	}
}
