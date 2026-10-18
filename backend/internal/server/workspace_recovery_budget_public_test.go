package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture follows src/server/api/sessions.checkpointBudget.test.ts:42-57:
// completed Agent records overflow activity recovery before a final Write.
// These are private replay bytes; no Agent or provider is invoked.
func workspaceRecoveryBudgetPublicFixture(t *testing.T, content string) (*httptest.Server, string, string) {
	t.Helper()
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("workspace history reads must not launch an Agent")
		return nil, nil
	})
	temporary := filepath.Join(options.HomeDir, "tmp")
	if err := os.MkdirAll(temporary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporary)
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(info.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	row := func(id, role string, content any) {
		t.Helper()
		value := subagentRow(t, id, role, "2026-10-01T08:00:00.000Z", content, map[string]any{"cwd": options.WorkDir})
		if _, err := writer.WriteString(value + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	row("workspace-budget-user", "user", "Synthetic history replay; inspect the file")
	description := strings.Repeat("a", 10*1024)
	for index := 0; index < 2050; index++ {
		toolID := fmt.Sprintf("agent-%d", index)
		row(toolID+"-call", "assistant", []any{map[string]any{"type": "tool_use", "id": toolID, "name": "Agent", "input": map[string]any{"description": description, "subagent_type": "general-purpose"}}})
		row(toolID+"-result", "user", []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": "Synthetic completed result"}})
	}
	name := id + ".txt"
	path := filepath.Join(options.WorkDir, name)
	row("workspace-budget-write", "assistant", []any{map[string]any{"type": "tool_use", "id": "write", "name": "Write", "input": map[string]any{"file_path": path, "content": content}}})
	row("workspace-budget-write-result", "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "write", "content": "Created successfully"}})
	row("workspace-budget-complete", "assistant", []any{map[string]any{"type": "text", "text": "Synthetic replay complete"}})
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(info.Path)
	if err != nil || stat.Size() <= 16*1024*1024 {
		t.Fatalf("fixture must exceed the unchanged full checkpoint budget: stat=%v error=%v", stat, err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return host, id, name
}

func TestWorkspaceRecoveryBudgetPublicRetainsSmallWriteAfterActivityOverflow(t *testing.T) {
	const content = "QA retained workspace evidence\n"
	host, id, name := workspaceRecoveryBudgetPublicFixture(t, content)
	base := "/api/sessions/" + id + "/"
	checkpoints := requestJSON(t, host, "GET", base+"turn-checkpoints", "", 413)
	if stringField(historyRecord(checkpoints), "error") != "HISTORY_CHECKPOINT_PREVIEW_LIMIT" {
		t.Fatal("large history must keep the full checkpoint rejection", string(checkpoints))
	}
	status := requestJSON(t, host, "GET", base+"workspace/status", "", 200)
	var result struct {
		State        string
		IsGitRepo    bool
		ChangedFiles []workspaceChange
	}
	if err := json.Unmarshal(status, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "ok" || result.IsGitRepo || len(result.ChangedFiles) != 1 {
		t.Fatal("bounded Write must remain available after activity overflow", string(status))
	}
	change := result.ChangedFiles[0]
	if change.Path != name || change.Status != "added" || change.Additions != 1 || change.Deletions != 0 {
		t.Fatal("workspace must expose exactly the retained file delta", string(status))
	}
	preview := requestJSON(t, host, "GET", base+"workspace/file?path="+name, "", 200)
	file := historyRecord(preview)
	if stringField(file, "state") != "ok" || stringField(file, "content") != content {
		t.Fatal("current file preview differs from its physical fixture", string(preview))
	}
	diff := requestJSON(t, host, "GET", base+"turn-checkpoints/diff?userMessageIndex=0&path="+name, "", 413)
	if stringField(historyRecord(diff), "error") != "HISTORY_CHECKPOINT_PREVIEW_LIMIT" {
		t.Fatal("current preview must not admit a full turn baseline", string(diff))
	}
}

func TestWorkspaceRecoveryBudgetPublicRejectsOversizedWriteAfterActivityOverflow(t *testing.T) {
	content := strings.Repeat("x", 128*1024)
	host, id, name := workspaceRecoveryBudgetPublicFixture(t, content)
	base := "/api/sessions/" + id + "/"
	status := requestJSON(t, host, "GET", base+"workspace/status", "", 413)
	if stringField(historyRecord(status), "error") != "HISTORY_WORKSPACE_LIMIT" {
		t.Fatal("oversized Write must retain the workspace evidence rejection", string(status))
	}
	preview := requestJSON(t, host, "GET", base+"workspace/file?path="+name, "", 200)
	var file struct {
		State string
		Size  int
	}
	if err := json.Unmarshal(preview, &file); err != nil || file.State != "ok" || file.Size != len(content) {
		t.Fatalf("readable current file must not turn incomplete Write evidence into a baseline: state=%q size=%d error=%v", file.State, file.Size, err)
	}
	checkpoints := requestJSON(t, host, "GET", base+"turn-checkpoints", "", 413)
	if stringField(historyRecord(checkpoints), "error") != "HISTORY_CHECKPOINT_PREVIEW_LIMIT" {
		t.Fatal("oversized Write must keep the full checkpoint rejection", string(checkpoints))
	}
}
