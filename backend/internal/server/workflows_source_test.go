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
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestWorkflowNativeHTTPHistoryAndResumeSidecarsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("Workflow history created a Harness")
		return nil, fmt.Errorf("no model allowed")
	})
	n, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: map[string]string{"HOME": options.HomeDir, "CLAUDE_CONFIG_DIR": options.ConfigDir, "DISABLE_TELEMETRY": "1"}, CWD: options.WorkDir, Platform: runtime.GOOS, ManagedRoot: filepath.Join(options.HomeDir, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clibootstrap.NewNativeProcessSettings(n, runtime.GOOS, clibootstrap.DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Close(context.Background())
	owner := cooperative.New()
	defer owner.Close()
	budget := sessionstore.NewBudget()
	options.HistoryBudget = budget
	var server *Server
	temp := filepath.Join(options.HomeDir, "tmp", "claude-"+strconv.Itoa(os.Getuid()))
	history, err := workflow.NewHistory(workflow.HistoryOptions{ConfigDir: options.ConfigDir, TempDir: temp, Files: workflow.NativeHistoryFiles(), Reader: sessionstore.NewReaderWithBudget(nil, budget), Notifications: func(ctx context.Context, id string) ([]protocol.JSONObject, error) {
		return server.WorkflowTaskNotifications(ctx, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	options.Workflows, err = settings.WorkflowDefinitions(owner, options.WorkDir, history)
	if err != nil {
		t.Fatal(err)
	}
	server, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	id := "00000000-0000-4000-8000-000000000901"
	root := ".claude/projects/private-history/" + id
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	millis := day.UnixMilli()
	runID := "wf_abc123"
	emptyRun := "wf_def456"
	script := `export const meta={name:'resumed',description:'Actual durable resume',phases:[{title:'first'}]};return 1`
	encode := func(value any) string { data, _ := json.Marshal(value); return string(data) }
	launch := func(run, task, tool, when string) map[string]any {
		return map[string]any{"type": "user", "sessionId": id, "uuid": "launch-" + run + when, "timestamp": when, "toolUseResult": map[string]any{"status": "async_launched", "runId": run, "taskId": task, "taskType": "local_workflow", "workflowName": "resumed"}, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": tool, "content": "launched"}}}}
	}
	notice := map[string]any{"type": "cc-haha-task-notification", "timestamp": day.Add(200 * time.Millisecond).Format(time.RFC3339Nano), "taskNotification": map[string]any{"taskId": "workflow-task", "toolUseId": "use-current", "workflowRunId": runID, "status": "failed", "summary": "actual error", "result": "{\"partial\":true}", "outputFile": "<private-home>/tmp/claude-" + strconv.Itoa(os.Getuid()) + "/private-project/" + id + "/tasks/workflow-task.output"}}
	transcript := encode(launch(runID, "workflow-task", "use-old", day.Format(time.RFC3339Nano))) + "\n" + encode(launch(runID, "workflow-task", "use-current", day.Add(100*time.Millisecond).Format(time.RFC3339Nano))) + "\n" + encode(notice) + "\n" + encode(launch(emptyRun, "empty-task", "empty-use", day.Add(300*time.Millisecond).Format(time.RFC3339Nano))) + "\n" + encode(map[string]any{"type": "cc-haha-task-notification", "timestamp": day.Add(400 * time.Millisecond).Format(time.RFC3339Nano), "taskNotification": map[string]any{"taskId": "empty-task", "toolUseId": "empty-use", "status": "completed", "result": "done without agents"}}) + "\n"
	sidecar := func(index int, label string) string {
		return encode(map[string]any{"description": label, "workflow": map[string]any{"runId": runID, "name": "resumed", "phaseIndex": 1, "phaseTitle": "first", "agentIndex": index}})
	}
	files := []map[string]any{{"path": ".claude/settings.json", "text": `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"future":{"keep":true}}`}, {"path": "work/.git/HEAD", "text": "ref: refs/heads/main\n"}, {"path": ".git/HEAD", "text": "ref: refs/heads/private-home\n"}, {"path": root + ".jsonl", "text": transcript}, {"path": root + "/workflows/resumed." + runID + ".js", "text": script, "mtime": millis}, {"path": root + "/subagents/workflows/" + runID + "/journal.jsonl", "text": "{\"type\":\"started\",\"key\":\"first\",\"agentId\":\"prefix\"}\n{\"type\":\"result\",\"key\":\"first\",\"agentId\":\"prefix\",\"result\":{\"real\":1}}\n{\"type\":\"started\",\"key\":\"second\",\"agentId\":\"current\"}\npartial-tail"}, {"path": root + "/subagents/agent-prefix.meta.json", "text": sidecar(1, "cached prefix"), "mtime": millis}, {"path": root + "/subagents/agent-current.meta.json", "text": sidecar(2, "current attempt"), "mtime": millis + 150}, {"path": root + "/subagents/agent-stale.meta.json", "text": sidecar(3, "stale tail must disappear"), "mtime": millis}, {"path": "tmp/claude-" + strconv.Itoa(os.Getuid()) + "/private-project/" + id + "/tasks/workflow-task.output", "text": encode(map[string]any{"workflowRunId": runID, "workflowProgress": []any{map[string]any{"type": "workflow_phase", "index": 1, "title": "first", "kind": "meta", "unknown": "drop"}, map[string]any{"type": "workflow_agent", "index": 1, "label": "cached prefix", "state": "done", "cached": true, "agentId": "prefix", "unknown": "drop"}, map[string]any{"type": "workflow_agent", "index": 2, "label": "current attempt", "state": "error", "error": "actual error", "tokens": 4, "toolCalls": 2, "blocked": false}}})}}
	seed := func(home string) {
		for _, row := range files {
			path := filepath.Join(home, row["path"].(string))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			text := strings.ReplaceAll(row["text"].(string), "<private-home>", home)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if ms, ok := row["mtime"].(int64); ok {
				when := time.UnixMilli(ms)
				if err := os.Chtimes(path, when, when); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	seed(options.HomeDir)
	operations := []map[string]any{{"kind": "runs", "sessionId": id}, {"kind": "run", "sessionId": id, "runId": runID}, {"kind": "reconstruct", "sessionId": id}, {"kind": "agent-state", "sessionId": id, "agentId": "current"}, {"kind": "run", "sessionId": id, "runId": "wf_missing"}, {"kind": "runs", "sessionId": "not-present"}, {"kind": "reconstruct", "sessionId": "not-present"}}
	actual := []map[string]any{}
	for _, op := range operations {
		var value any
		var e error
		switch op["kind"] {
		case "runs":
			id := op["sessionId"].(string)
			value, e = options.Workflows.ListRuns(context.Background(), &id, nil)
		case "run":
			value, e = options.Workflows.GetRun(context.Background(), op["sessionId"].(string), op["runId"].(string))
		case "reconstruct":
			value, e = options.Workflows.ReconstructSessionRuns(context.Background(), op["sessionId"].(string))
		case "agent-state":
			raw, err := options.Workflows.GetAgentRunState(context.Background(), op["sessionId"].(string), op["agentId"].(string))
			e = err
			_ = json.Unmarshal(raw, &value)
		}
		if e != nil {
			api, ok := e.(*workflow.APIError)
			if !ok {
				t.Fatal(e)
			}
			actual = append(actual, map[string]any{"status": api.Status, "code": api.Code, "error": api.Message})
		} else {
			actual = append(actual, map[string]any{"result": value})
		}
	}
	sourceHome := t.TempDir()
	sourceHome, _ = filepath.EvalSymlinks(sourceHome)
	seed(sourceHome)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "cwd": "work", "files": files, "operations": operations})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source %v %.2500s %.2500s", e, stdout.String(), stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.Observations) != len(actual) {
		t.Fatal(e, source.Capabilities.Blocked, len(source.Observations), stderr.String())
	}
	normalize := func(raw []byte, home string) any {
		raw = bytes.ReplaceAll(raw, []byte(home), []byte("<private-home>"))
		var value any
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e, string(raw))
		}
		return value
	}
	for i, value := range actual {
		raw, _ := json.Marshal(value)
		if !reflect.DeepEqual(normalize(raw, options.HomeDir), normalize(source.Observations[i], sourceHome)) {
			t.Fatalf("Source history mismatch%d Go=%s\nSource=%s", i, strings.ReplaceAll(string(raw), options.HomeDir, "<private-home>"), strings.ReplaceAll(string(source.Observations[i]), sourceHome, "<private-home>"))
		}
	}
	for _, path := range []string{"/api/workflows/runs?sessionId=" + id + "&limit=1suffix", "/api/workflows/runs/" + id + "/" + runID, "/api/workflows/session-runs/" + id} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:3456"+path, nil)
		request.RemoteAddr = "127.0.0.1:49000"
		server.ServeHTTP(response, request)
		if response.Code != 200 || strings.Contains(response.Body.String(), "stale tail must disappear") {
			t.Fatalf("actual HTTP workflow contract %s => %d %s", path, response.Code, response.Body.String())
		}
	}
}
