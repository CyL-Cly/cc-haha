package clibootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualMainWorkerTaskCreatedRollbackAndCompletedVetoUseRealHookProcess(t *testing.T) {
	names := []string{"TaskCreate", "TaskUpdate", "TaskGet", "TaskList"}
	m, f, _ := teamWorkerFixture(t, teamworker.Start{ParentSessionID: "parent", TeamName: "actual-team", MemberID: "m", Name: "worker", SystemPrompt: "actual", Tools: &names, AgentDefinition: protocol.NewJSONObject()}, "http://127.0.0.1:1")
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	if err := m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.cwd, "actual-task-hook-input.json")
	command := "cat > '" + strings.ReplaceAll(path, "'", "'\\''") + "'; echo ACTUAL_TASK_VETO >&2; exit 2"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	install := func(event, body string) {
		t.Helper()
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			m.hook.ClearNativeAgentHooks(m.Controller.SessionID())
			return m.hook.RegisterNativeMainHooks(m.Controller.SessionID(), protocol.NewJSONObject(protocol.JSONField{Name: event, Value: rawMain([]any{map[string]any{"hooks": []any{map[string]string{"type": "command", "command": body}}}})}))
		}); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name, input string) (protocol.JSONObject, error) {
		var output protocol.JSONObject
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			_, tools, err := m.nativeTools.Catalog(scope)
			if err != nil {
				return err
			}
			for _, tool := range tools {
				if tool.Name != name {
					continue
				}
				parsed, err := tool.ParseInput(objMain([]byte(input)))
				if err != nil {
					return err
				}
				future, err := tool.StartCallJSON(ctx, parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope, AgentID: m.options.AgentID}})
				if err != nil {
					return err
				}
				result, err := future.Await(scope)
				if err != nil {
					return err
				}
				return output.UnmarshalJSON(result.Data)
			}
			return errors.New("actual task not registered")
		})
		return output, err
	}
	install("TaskCreated", command)
	if _, err := call("TaskCreate", `{"subject":"rollback","description":"actual hook"}`); err == nil || !strings.Contains(err.Error(), "TaskCreated hook feedback:\n") {
		t.Fatalf("created veto missing %v", err)
	}
	tasks, err := m.taskStore.List("actual-team")
	if err != nil || len(tasks) != 0 {
		t.Fatal("creation not rolled back", err, tasks)
	}
	check := func(event string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var input protocol.JSONObject
		if err = input.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
		if input.String("hook_event_name") != event || input.String("session_id") != m.Controller.SessionID() || input.String("team_name") != "actual-team" || input.String("teammate_name") != "worker" || input.String("agent_type") != "worker" || input.Get("agent_id") != nil || input.Get("permission_mode") != nil || input.String("task_subject") == "" {
			t.Fatalf("original Task hook input mismatch %s", data)
		}
	}
	check("TaskCreated")
	install("TaskCreated", "exit 0")
	created, err := call("TaskCreate", `{"subject":"completed veto","description":"actual second task"}`)
	if err != nil {
		t.Fatal(err)
	}
	var task protocol.JSONObject
	_ = task.UnmarshalJSON(created.Get("task"))
	id := task.String("id")
	install("TaskCompleted", command)
	result, err := call("TaskUpdate", string(rawMain(map[string]string{"taskId": id, "status": "completed"})))
	if err != nil || string(result.Get("success")) != "false" || !strings.Contains(result.String("error"), "TaskCompleted hook feedback:\n") {
		t.Fatalf("completed veto missing %v %s", err, rawMain(result))
	}
	check("TaskCompleted")
	actual, err := m.taskStore.Get("actual-team", quoteMain(id))
	if err != nil || actual == nil || actual.String("status") != "pending" {
		t.Fatal("veto committed completion", err, actual)
	}
	install("TaskCompleted", "exit 0")
	result, err = call("TaskUpdate", string(rawMain(map[string]string{"taskId": id, "status": "completed"})))
	if err != nil || string(result.Get("success")) != "true" {
		t.Fatal(err, string(rawMain(result)))
	}
	actual, err = m.taskStore.Get("actual-team", quoteMain(id))
	if err != nil || actual == nil || actual.String("status") != "completed" {
		t.Fatal("success not committed", err, actual)
	}
}
