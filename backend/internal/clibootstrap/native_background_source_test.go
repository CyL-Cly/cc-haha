package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func taskExact(a, b json.RawMessage) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
func TestActualTaskOutputStopConsumerMatchesOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := m.nativeTaskOutputDirectory(scope); return e }); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, status := range []string{"running", "pending", "completed", "failed", "killed", "paused"} {
		for _, block := range []bool{false, true} {
			input, _ := json.Marshal(map[string]any{"task_id": "bfixture", "block": block, "timeout": 0})
			rows = append(rows, map[string]any{"name": "TaskOutput", "input": string(input), "task": map[string]any{"id": "bfixture", "type": "local_bash", "status": status, "description": "source shell", "notified": false, "result": map[string]any{"code": 7}}, "output": "actual stderr\nactual stdout\ufeff"})
		}
	}
	for _, answer := range []string{"", "clean final"} {
		rows = append(rows, map[string]any{"name": "TaskOutput", "input": `{"task_id":"afixture","block":false}`, "task": map[string]any{"id": "afixture", "type": "local_agent", "status": "completed", "description": "source agent", "notified": false, "prompt": "original prompt", "error": "source failure", "result": map[string]any{"content": []map[string]any{{"type": "text", "text": answer}}}}, "output": "{\"actual\":\"transcript\"}\n"})
	}
	for _, task := range []map[string]any{nil, {"id": "bfixture", "type": "local_bash", "status": "running", "description": "Stop source", "command": "printf exact", "notified": false}, {"id": "bfixture", "type": "local_bash", "status": "completed", "description": "done", "notified": false}, {"id": "bfixture", "type": "unknown", "status": "running", "description": "unknown", "notified": false}} {
		rows = append(rows, map[string]any{"name": "TaskStop", "input": `{"task_id":"bfixture"}`, "task": task})
	}
	rows = append(rows, map[string]any{"name": "TaskOutput", "input": `{"task_id":"missing","timeout":0}`, "task": nil}, map[string]any{"name": "TaskStop", "input": `{}`, "task": nil})
	maps := []map[string]any{}
	for _, limit := range []string{""} {
		data := `{"retrieval_status":"success","task":{"task_id":"bfixture","task_type":"local_bash","status":"failed","exitCode":7,"output":"  \ud800😀abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz\ufeff ","error":"actual error"}}`
		maps = append(maps, map[string]any{"name": "TaskOutput", "limit": limit, "data": data})
	}
	maps = append(maps, map[string]any{"name": "TaskOutput", "data": `{"retrieval_status":"timeout","task":null}`}, map[string]any{"name": "TaskStop", "data": `{"message":"exact","task_id":"bfixture","task_type":"local_bash"}`})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "calls": rows, "maps": maps})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2400s", e, out)
	}
	var source struct {
		OutputDir    string
		Mapped       []protocol.JSONObject
		Calls        []protocol.JSONObject
		Validations  []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Calls) != len(rows) || len(source.Mapped) != len(maps) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.2500s", e, out)
	}
	if source.OutputDir != m.taskOutputDirectory {
		t.Fatalf("original output path Go%s Source%s", m.taskOutputDirectory, source.OutputDir)
	}
	e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		byName := map[string]queryloop.RegisteredTool{}
		for _, tool := range tools {
			byName[tool.Name] = tool
		}
		for i, row := range rows {
			var taskID string
			if original, ok := row["task"].(map[string]any); ok && original != nil {
				task := taskObject(t, rawMain(original))
				taskID = task.String("id")
				_ = m.Controller.DeleteBackgroundTaskOnOwner(scope, taskID)
				if e = m.Controller.RegisterBackgroundTaskOnOwner(scope, &sessioncontroller.BackgroundTask{Data: task, Cancel: func(error) {}, ReadOutput: func(context.Context) (json.RawMessage, error) {
					value, _ := row["output"].(string)
					return quoteMain(value), nil
				}, Join: func(context.Context) error { return nil }}); e != nil {
					return e
				}
			} else {
				_ = m.Controller.DeleteBackgroundTaskOnOwner(scope, "bfixture")
			}
			tool := byName[row["name"].(string)]
			in := taskObject(t, []byte(row["input"].(string)))
			parsed, e := tool.ParseInput(in)
			if e != nil {
				return e
			}
			validation := tool.ValidateInput(context.Background(), parsed, &builtinwrite.CallContext{Scope: scope})
			if validation == nil {
				if string(source.Validations[i].Get("result")) != "true" {
					t.Errorf("validation%d Go success Source%s", i, rawMain(source.Validations[i]))
				}
			} else if validation.Error() != source.Validations[i].String("message") {
				t.Errorf("validation%d Go%s Source%s", i, validation, rawMain(source.Validations[i]))
			}
			result, failure := tool.CallJSON(context.Background(), parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quoteMain("actual-task-use")})
			if failure != nil {
				if failure.Error() != source.Calls[i].String("error") {
					t.Errorf("call%d Go%s Source%s", i, failure, rawMain(source.Calls[i]))
				}
				continue
			}
			if !taskExact(result.Data, source.Calls[i].Get("data")) {
				t.Errorf("call%d Go%s Source%s", i, result.Data, source.Calls[i].Get("data"))
			}
			entry, e := m.Controller.BackgroundTaskOnOwner(scope, taskID)
			if e != nil {
				return e
			}
			if entry != nil && !taskExact(entry.Data.Get("notified"), source.Calls[i].Get("notified")) {
				t.Errorf("notified%d Go%s Source%s", i, entry.Data.Get("notified"), source.Calls[i].Get("notified"))
			}
		}
		for i, row := range maps {
			tool := byName[row["name"].(string)]
			got, e := tool.MapResultJSON([]byte(row["data"].(string)), quoteMain("actual-task-use"))
			if e != nil {
				return e
			}
			if !taskExact(rawMain(got), rawMain(source.Mapped[i])) {
				t.Errorf("map%d Go%s Source%s", i, rawMain(got), rawMain(source.Mapped[i]))
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
