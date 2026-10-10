package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
)

func TestScheduledTasksHTTPActualCRUDManualLogSessionAndSummaryProjection(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	var owned *Server
	paths := map[string]string{}
	var pathsMu sync.Mutex
	var executorCalls atomic.Int32
	inputs := make(chan scheduler.ExecutionRequest, 4)
	service, e := scheduler.New(scheduler.Options{Home: o.HomeDir, ConfigDir: o.ConfigDir, Environment: func() map[string]string { return map[string]string{} }, Now: time.Now, RandomBytes: func(n int) ([]byte, error) { b := make([]byte, n); _, e := rand.Read(b); return b, e }, GetProcessEnvironment: func(context.Context) (map[string]string, error) {
		return map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake"}, nil
	}, ProviderRuntimeEnv: func(context.Context, string) (map[string]string, error) {
		return map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake"}, nil
	}, NetworkEnvironment: func(context.Context, map[string]string) (map[string]string, error) { return map[string]string{}, nil }, AgentTeamsEnabled: func(context.Context) (bool, error) { return false, nil }, OfficialOAuthEnv: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, CreateSession: func(ctx context.Context, cwd, mode string) (string, error) {
		return owned.CreateScheduledSession(ctx, cwd, mode)
	}, DeletePlaceholder: func(ctx context.Context, id string) error {
		info, e := owned.findSession(ctx, id)
		if e != nil {
			return e
		}
		pathsMu.Lock()
		paths[id] = info.Path
		pathsMu.Unlock()
		return owned.DeleteScheduledPlaceholder(ctx, id)
	}, AppendSessionMetadata: func(ctx context.Context, id string, value protocol.JSONObject) error {
		return owned.AppendScheduledSessionMetadata(ctx, id, value)
	}, LogError: func(e error) { t.Log(e) }, Execute: func(ctx context.Context, request scheduler.ExecutionRequest, emit func(json.RawMessage)) (scheduler.ExecutionResult, error) {
		executorCalls.Add(1)
		inputs <- request
		pathsMu.Lock()
		path := paths[request.SessionID]
		pathsMu.Unlock()
		if path == "" {
			t.Error("actual placeholder path missing")
		}
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(request.Input.Get("message"))
		var content []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &content)
		// This test owns only transport/dispatcher boundaries. The actual shared
		// Harness and loopback-provider invocation is application E2E evidence.
		text := "derived:" + content[0].String("text")
		row := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"user"`)}, protocol.JSONField{Name: "uuid", Value: []byte(`"actual-scheduled-first-user"`)}, protocol.JSONField{Name: "sessionId", Value: request.Input.Get("session_id")}, protocol.JSONField{Name: "cwd", Value: []byte(strconvQuote(request.WorkDir))}, protocol.JSONField{Name: "timestamp", Value: []byte(`"2026-10-07T00:00:00.000Z"`)}, protocol.JSONField{Name: "message", Value: request.Input.Get("message")})
		b, _ := row.MarshalJSON()
		if e := os.WriteFile(path, append(b, '\n'), 0600); e != nil {
			return scheduler.ExecutionResult{}, e
		}
		owned.NotifyCanonicalTranscriptMutation(path)
		output, _ := json.Marshal(map[string]string{"type": "result", "result": text})
		emit(output)
		return scheduler.ExecutionResult{}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	o.Scheduler = service
	owned, host := testHost(t, o)
	defer service.Stop(context.Background())
	call := func(method, path, body string) (int, map[string]json.RawMessage) {
		t.Helper()
		req, e := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		response, e := host.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		var result map[string]json.RawMessage
		if e = json.Unmarshal(b, &result); e != nil {
			t.Fatal(e, string(b))
		}
		return response.StatusCode, result
	}
	code, result := call("POST", "/api/scheduled-tasks", `{"cron":"* * * * *","prompt":"HTTP manual task","enabled":false,"permissionMode":"plan","folderPath":`+strconvQuote(o.WorkDir)+`,"providerId":"saved"}`)
	if code != 201 {
		t.Fatal(code, result)
	}
	var task protocol.JSONObject
	_ = task.UnmarshalJSON(result["task"])
	id := task.String("id")
	if task.String("permissionMode") != "bypassPermissions" {
		t.Fatal(task)
	}
	code, result = call("PUT", "/api/scheduled-tasks/"+id, `{"enabled":true,"recurring":false,"permanent":true,"futurePreserved":1}`)
	if code != 200 {
		t.Fatal(code, result)
	}
	code, result = call("POST", "/api/scheduled-tasks/"+id+"/run", "")
	if code != 200 || string(result["ok"]) != "true" {
		t.Fatal(code, result)
	}
	var request scheduler.ExecutionRequest
	select {
	case request = <-inputs:
	case <-time.After(time.Second):
		t.Fatal("manual route did not execute")
	}
	if request.SessionID == "" || request.WorkDir != o.WorkDir || request.Input.String("session_id") != request.SessionID {
		t.Fatal("shared execution identity", request)
	}
	deadline := time.Now().Add(2 * time.Second)
	var runs []protocol.JSONObject
	for {
		code, result = call("GET", "/api/scheduled-tasks/"+id+"/runs", "")
		_ = json.Unmarshal(result["runs"], &runs)
		if len(runs) == 1 && runs[0].String("status") == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed actual log not visible", result)
		}
		time.Sleep(time.Millisecond)
	}
	if runs[0].String("output") != "derived:HTTP manual task" || runs[0].String("sessionId") != request.SessionID || executorCalls.Load() != 1 {
		t.Fatal("actual result lost", runs)
	}
	code, result = call("GET", "/api/scheduled-tasks/runs?summaryOnly=true", "")
	_ = json.Unmarshal(result["runs"], &runs)
	if code != 200 || len(runs) != 1 || runs[0].Get("prompt") != nil || runs[0].Get("output") != nil || string(runs[0].Get("hasOutput")) != "true" {
		t.Fatal("poll leaked bodies", code, result)
	}
	if len(result["revision"]) == 0 || !strings.Contains(string(result["revisionToken"]), "sqlite:") {
		t.Fatal("actual SQLite read model not bound", result)
	}
	code, result = call("GET", "/api/scheduled-tasks", "")
	var tasks []protocol.JSONObject
	_ = json.Unmarshal(result["tasks"], &tasks)
	if code != 200 || len(tasks) != 1 || string(tasks[0].Get("enabled")) != "false" || string(tasks[0].Get("futurePreserved")) != "1" {
		t.Fatal("non-recurring disable or mutation lost", result)
	}
	info, e := owned.findSession(context.Background(), request.SessionID)
	if e != nil || info.PermissionMode != "bypassPermissions" || info.WorkDir != o.WorkDir {
		t.Fatal("real session metadata not visible", info, e)
	}
	code, result = call("DELETE", "/api/scheduled-tasks/"+id, "")
	if code != 200 || string(result["ok"]) != "true" {
		t.Fatal(code, result)
	}
	code, _ = call("POST", "/api/scheduled-tasks/"+id+"/run", "")
	if code != 404 {
		t.Fatal("deleted task resurrected", code)
	}
}
