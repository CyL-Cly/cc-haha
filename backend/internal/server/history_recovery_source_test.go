package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHistoryRecoveryStateBudgetsOwnershipUsageAndCacheMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	entry := func(kind, id string, content any, extra map[string]any) string {
		value := map[string]any{"type": kind, "uuid": id, "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": kind, "content": content}}
		for key, item := range extra {
			value[key] = item
		}
		data, _ := json.Marshal(value)
		return string(data) + "\n"
	}
	tool := func(id, name string, input any) any {
		return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
	}
	result := func(id string, content any) any {
		return map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
	}
	goal := func(id, output string) string {
		data, _ := json.Marshal(map[string]any{"type": "system", "subtype": "local_command", "uuid": id, "content": "<local-command-stdout>" + output + "</local-command-stdout>", "timestamp": "2026-01-01T00:00:00.000Z"})
		return string(data) + "\n"
	}
	rows := []map[string]any{}
	add := func(name, content string, appendText *string) {
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", len(rows)+1)
		row := map[string]any{"name": name, "id": id, "content": content}
		if appendText != nil {
			row["append"] = *appendText
		}
		rows = append(rows, row)
	}
	add("empty", "", nil)
	add("goal-latest-clears-status", goal("g1", "Goal set: finish")+goal("g2", "Goal continuing: still working")+goal("g3", "Goal cleared.")+entry("user", "human", "new task", nil), nil)
	add("todo-task-latest", entry("assistant", "todo1", []any{tool("todo-use", "TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "old", "status": "pending"}}})}, nil)+entry("assistant", "todo2", []any{tool("todo-use2", "TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "new", "status": "completed"}}})}, nil)+entry("assistant", "task", []any{tool("task-use", "TaskList", map[string]any{})}, nil)+entry("user", "human", "next", nil), nil)
	add("child-ownership", entry("assistant", "root", []any{tool("owner", "Agent", map[string]any{"description": "inspect", "subagent_type": "worker", "run_in_background": true})}, nil)+entry("assistant", "child", []any{tool("shell", "Bash", map[string]any{"command": "sleep 1", "run_in_background": true})}, map[string]any{"parent_tool_use_id": "owner"})+entry("user", "child-result", []any{result("shell", "Command running in background with ID: hidden")}, map[string]any{"parentUuid": "child", "isSidechain": true}), nil)
	for _, structured := range []bool{false, true} {
		extra := map[string]any{}
		if structured {
			extra["toolUseResult"] = map[string]any{"background_task_id": "job_123"}
		}
		add(fmt.Sprintf("shell-background-%v", structured), entry("assistant", "shell", []any{tool("bash", "Bash", map[string]any{"command": "sleep 1", "description": "worker", "run_in_background": true})}, nil)+entry("user", "shell-result", []any{result("bash", "Command running in background with ID: job_123")}, extra), nil)
	}
	add("foreground-output-not-activity", entry("assistant", "shell", []any{tool("bash", "Bash", map[string]any{"command": "cat file"})}, nil)+entry("user", "shell-result", []any{result("bash", strings.Repeat("x", 256*1024))}, nil), nil)
	add("workspace-real-results", entry("assistant", "edit", []any{tool("w1", "Write", map[string]any{"file_path": "/private/output", "content": "written"})}, nil)+entry("user", "edit-result", []any{result("w1", "created")}, map[string]any{"toolUseResult": map[string]any{"type": "create", "filePath": "/private/output", "content": "written"}}), nil)
	add("mixed-workspace-agent-incomplete", entry("assistant", "mixed", []any{tool("w1", "Write", map[string]any{"file_path": "/private/output", "content": "write"}), tool("agent1", "Agent", map[string]any{"description": "agent"})}, nil), nil)
	add("large-workspace-independent", entry("assistant", "todo", []any{tool("todo1", "TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "still valid", "status": "pending"}}})}, nil)+entry("assistant", "write", []any{tool("w1", "Write", map[string]any{"file_path": "/private/output", "content": strings.Repeat("x", 128*1024)})}, nil), nil)
	add("oversized-record-all-incomplete", entry("assistant", "large", strings.Repeat("x", 9*1024*1024), nil), nil)
	add("bad-record-all-incomplete", "{invalid\n"+entry("user", "human", "visible", nil), nil)
	add("notice-latest-and-teammates", entry("user", "notice", "<task-notification><task-id>job</task-id><tool-use-id>tool</tool-use-id><status>completed</status><summary>finished</summary></task-notification>", nil)+entry("user", "teammate", "<teammate-message teammate_id=\"worker\">first result</teammate-message>\n<teammate-message teammate_id=\"worker\">later ignored</teammate-message>", nil), nil)
	usage := map[string]any{"input_tokens": 10, "output_tokens": 20, "cache_read_input_tokens": 5, "cache_creation_input_tokens": 3}
	duplicate := ""
	for i := 0; i < 4; i++ {
		duplicate += entry("assistant", fmt.Sprintf("u%d", i), nil, map[string]any{"message": map[string]any{"id": "one-reply", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 256*1024)}}, "usage": usage}, "requestId": "one-request"})
	}
	add("usage-dedup-and-fork", duplicate+entry("assistant", "fork", nil, map[string]any{"message": map[string]any{"id": "fork-reply", "role": "assistant", "content": "inherited", "usage": usage}, "forkedFrom": map[string]any{"sessionId": "parent", "messageUuid": "source"}}), nil)
	appendText := entry("user", "appended", "next user", nil)
	add("cache-invalidated-by-append", entry("user", "original", "first", nil), &appendText)
	largeTodo := entry("assistant", "oversized-todo", []any{tool("todo", "TodoWrite", map[string]any{"todos": []any{map[string]any{"content": strings.Repeat("x", 64*1024), "status": "pending"}}})}, nil)
	add("todo-record-budget", largeTodo, nil)
	wide := ""
	for i := 0; i < 2100; i++ {
		wide += entry("assistant", fmt.Sprintf("a%d", i), []any{tool(fmt.Sprintf("agent%d", i), "Agent", map[string]any{"description": "task"})}, nil)
	}
	add("activity-count-bound", wide, nil)
	wide = ""
	for i := 0; i < 850; i++ {
		wide += entry("assistant", fmt.Sprintf("d%d", i), []any{tool(fmt.Sprintf("detailed%d", i), "Agent", map[string]any{"description": strings.Repeat("x", 4096)})}, nil)
	}
	add("activity-byte-budget", wide, nil)
	add("utf16-brief-surrogate-boundary", entry("user", "teammate", "<teammate-message teammate_id=\"worker\">x"+strings.Repeat("😀", 2048)+"</teammate-message>", nil), nil)
	firstID := rows[0]["id"].(string)
	missingID := "11111111-1111-4111-8111-999999999999"
	httpCases := []map[string]any{
		{"id": firstID},
		{"id": rows[len(rows)-1]["id"]},
		{"id": rows[9]["id"]},
		{"id": missingID},
		{"id": firstID, "method": "POST"},
		{"id": missingID, "method": "POST"},
		{"id": missingID, "method": "PUT"},
		{"id": firstID, "method": "PATCH", "body": `{}`},
		{"id": firstID, "method": "PATCH", "body": `{"title":"renamed through item route"}`},
		{"id": firstID, "extra": []string{"ignored-source-suffix"}},
		{"id": firstID, "method": "DELETE"},
		{"id": firstID},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-history-recovery.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HISTORY_RECOVERY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "cases": rows, "httpCases": httpCases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Results []struct {
			Name        string
			Observation struct {
				First                                json.RawMessage
				After                                json.RawMessage
				SameCachedObject, ChangedCacheObject bool
			}
		}
		Capabilities struct{ Blocked []string }
		HTTPResults  []struct {
			Status int
			Body   json.RawMessage
		}
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output)[:min(len(output), 500)])
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal(source.Capabilities.Blocked)
	}
	if len(source.Results) != len(rows) {
		t.Fatal(len(source.Results), len(rows))
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("Recovery must not start a model")
		return nil, nil
	})
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	o.HomeDir = home
	o.ConfigDir = filepath.Join(home, ".claude")
	o.WorkDir = home
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	compare := func(name string, want, got json.RawMessage) {
		t.Helper()
		var expected, actual any
		_ = json.Unmarshal(want, &expected)
		_ = json.Unmarshal(got, &actual)
		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("%s Source%s Go%s", name, want[:min(len(want), 1500)], got[:min(len(got), 1500)])
		}
	}
	for i, row := range rows {
		id := row["id"].(string)
		path := filepath.Join(home, ".claude", "projects", "private", id+".jsonl")
		if e = os.WriteFile(path, []byte(row["content"].(string)), 0600); e != nil {
			t.Fatal(e)
		}
		info, e := s.findSession(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		first, e := s.recoverSessionHistory(ctx, info)
		if e != nil {
			t.Fatal(e)
		}
		want := source.Results[i].Observation
		// Source and Go read the same actual private file. Only its mutable stat
		// version changes when this test restores the pre-append input bytes.
		expected := historyRecord(want.First)
		actual := historyRecord(first)
		expected["sourceVersion"] = actual["sourceVersion"]
		compare(row["name"].(string), recoveryJSON(expected), first)
		cacheBefore := s.historyRecoveryCache[id]
		cached, e := s.recoverSessionHistory(ctx, info)
		if e != nil {
			t.Fatal(e)
		}
		if !want.SameCachedObject || !bytes.Equal(cacheBefore.data, cached) {
			t.Fatal("Original cache observation differs")
		}
		if appended, ok := row["append"].(string); ok {
			f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.WriteString(appended)
			_ = f.Close()
			if e != nil {
				t.Fatal(e)
			}
			after, e := s.recoverSessionHistory(ctx, info)
			if e != nil {
				t.Fatal(e)
			}
			expected := historyRecord(want.After)
			actual := historyRecord(after)
			expected["sourceVersion"] = actual["sourceVersion"]
			compare(row["name"].(string)+" append", recoveryJSON(expected), after)
			if !want.ChangedCacheObject || s.historyRecoveryCache[id].version == cacheBefore.version {
				t.Fatal("Append did not invalidate Source cache")
			}
		}
	}
	if len(s.historyRecoveryCache) != 2 || len(s.historyRecoveryOrder) != 2 {
		t.Fatal("Recovery cache exceeded original two entries")
	}
	if len(source.HTTPResults) != len(httpCases) {
		t.Fatal("Missing actual Source HTTP observations", len(source.HTTPResults))
	}
	for i, row := range httpCases {
		method, _ := row["method"].(string)
		if method == "" {
			method = "GET"
		}
		body, _ := row["body"].(string)
		path := "/api/sessions/" + row["id"].(string) + "/history-recovery"
		if extra, ok := row["extra"].([]string); ok {
			path += "/" + strings.Join(extra, "/")
		}
		request := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request)
		want := source.HTTPResults[i]
		if w.Code != want.Status {
			t.Errorf("HTTP%d %s: Source status%d Go%d body%s", i, method, want.Status, w.Code, w.Body)
		}
		expected := historyRecord(want.Body)
		actual := historyRecord(w.Body.Bytes())
		if expected["sourceVersion"] != nil {
			expected["sourceVersion"] = actual["sourceVersion"]
		}
		compare(fmt.Sprintf("HTTP%d %s", i, method), recoveryJSON(expected), w.Body.Bytes())
	}
	files, _ := os.ReadDir(filepath.Join(home, "tmp"))
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "claude-history-recovery-") {
			t.Error("Source left recovery scratch DB", file.Name())
		}
	}
}
