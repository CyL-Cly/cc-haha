package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerExecutionActualSourcePhysicalChildInputLogsDisableAndMetadata(t *testing.T) {
	rows := []map[string]any{}
	for n, mode := range []string{"completed", "failed", "timeout", "exception", "session-fallback"} {
		row := map[string]any{"task": map[string]any{"id": "task", "cron": "* * * * *", "name": "scheduled", "prompt": "actual " + mode, "enabled": true, "recurring": n%2 == 0, "model": " deepseek-flash ", "useWorktree": true}, "exitCode": 0, "duration": 100, "manual": n != 2}
		switch mode {
		case "failed":
			row["exitCode"] = 2
		case "timeout":
			row["duration"] = 1000
			row["timeout"] = float64(500)
			row["exitCode"] = 143
		case "exception":
			row["failure"] = "execution fixture failed"
		case "session-fallback":
			row["createFailure"] = true
		}
		rows = append(rows, row)
	}
	data := actualSource(t, "execution", map[string]any{"now": int64(1700000000000), "rows": rows})
	var source struct {
		HomeDir string
		Rows    []struct {
			Run, Log, Input json.RawMessage
			Tasks           json.RawMessage
			Metadata        []struct {
				Sid   string
				Value protocol.JSONObject
			}
			Launch struct{ Args []string }
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	random := 0
	now := int64(1700000000000)
	for n, row := range rows {
		o := disposableOptions(t)
		canonicalHome, _ := filepath.EvalSymlinks(o.Home)
		o.Now = func() time.Time { return time.UnixMilli(now) }
		o.RandomBytes = func(n int) ([]byte, error) {
			random++
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(random)
			}
			return b, nil
		}
		o.Environment = func() map[string]string {
			timeout := 600000
			if row["timeout"] != nil {
				timeout = 500
			}
			return map[string]string{"CC_HAHA_LOCAL_INDEX": "off", "CC_HAHA_TASK_TIMEOUT_MS": string(raw(timeout))}
		}
		var input protocol.JSONObject
		var metadata []protocol.JSONObject
		var metadataSID []string
		createCalls := 0
		o.CreateSession = func(_ context.Context, cwd, mode string) (string, error) {
			createCalls++
			if cwd != canonicalHome || mode != "bypassPermissions" {
				t.Error("manual launch scope", cwd, mode)
			}
			if row["createFailure"] == true {
				return "", errors.New("session fixture failed")
			}
			return "00000000-0000-4000-8000-000000000001", nil
		}
		o.AppendSessionMetadata = func(_ context.Context, sid string, value protocol.JSONObject) error {
			metadataSID = append(metadataSID, sid)
			metadata = append(metadata, value)
			return nil
		}
		o.Execute = func(_ context.Context, request ExecutionRequest, emit func(json.RawMessage)) (ExecutionResult, error) {
			input = request.Input.Clone()
			if request.WorkDir != canonicalHome || request.Task.String("model") != " deepseek-flash " {
				t.Error("runtime model/workdir lost", request)
			}
			var msg protocol.JSONObject
			_ = msg.UnmarshalJSON(request.Input.Get("message"))
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(msg.Get("content"), &blocks)
			prompt := blocks[0].String("text")
			emit(raw(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "thinking", "thinking": "hidden"}, {"type": "text", "text": "  child:" + prompt + "  "}, {"type": "tool_use", "name": "Read", "input": map[string]any{}}}}}))
			emit(raw(map[string]any{"type": "result", "result": "child:" + prompt}))
			now += int64(row["duration"].(int))
			result := ExecutionResult{ExitCode: row["exitCode"].(int), Stderr: "error:" + prompt + "\n"}
			if failure, ok := row["failure"].(string); ok {
				return result, errors.New(failure)
			}
			return result, nil
		}
		s, e := New(o)
		if e != nil {
			t.Fatal(e)
		}
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(raw(row["task"]))
		task.Set("folderPath", quoted(o.Home))
		_ = os.MkdirAll(filepath.Join(o.ConfigDir, "cc-haha"), 0700)
		_ = os.WriteFile(filepath.Join(o.ConfigDir, "cc-haha/settings.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"fake"}}`), 0600)
		_ = os.WriteFile(s.store.path(), raw(map[string]any{"tasks": []protocol.JSONObject{task}}), 0600)
		_ = os.WriteFile(s.store.runsPath(), []byte(`{"future":"keep","runs":[]}`), 0600)
		run, e := s.ExecuteTask(context.Background(), task, row["manual"].(bool))
		if e != nil {
			t.Fatal(e)
		}
		if !equalJSON(raw(run), source.Rows[n].Run) {
			t.Errorf("run%d Go%s Source%s", n, raw(run), source.Rows[n].Run)
		}
		if !equalJSON(raw(input), source.Rows[n].Input) {
			t.Errorf("input%d Go%s Source%s", n, raw(input), source.Rows[n].Input)
		}
		tasks, e := s.ListTasks(context.Background())
		expected := strings.ReplaceAll(string(source.Rows[n].Tasks), source.HomeDir, o.Home)
		if e != nil || !equalJSON(raw(tasks), []byte(expected)) {
			t.Errorf("tasks%d Go%s Source%s %v", n, raw(tasks), expected, e)
		}
		log, e := os.ReadFile(s.store.runsPath())
		if e != nil || !equalJSON(log, source.Rows[n].Log) {
			t.Errorf("log%d Go%s Source%s %v", n, log, source.Rows[n].Log, e)
		}
		if len(metadata) != len(source.Rows[n].Metadata) {
			t.Fatal("metadata count", n, len(metadata), source.Rows[n].Metadata)
		}
		for j, value := range metadata {
			want := strings.ReplaceAll(string(raw(source.Rows[n].Metadata[j].Value)), source.HomeDir, o.Home)
			if metadataSID[j] != source.Rows[n].Metadata[j].Sid || !equalJSON(raw(value), []byte(want)) {
				t.Error("metadata scope", value, want)
			}
		}
		if row["manual"] == true && createCalls != 1 || row["manual"] == false && createCalls != 0 {
			t.Fatal("automatic cron created public sidebar session", n, createCalls)
		}
		if e = s.Stop(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}
