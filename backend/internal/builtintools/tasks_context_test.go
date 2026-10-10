package builtintools

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"testing"
)

func TestActualTaskStoreAsyncHookReceivesSameOriginalToolCallContext(t *testing.T) {
	s, owner, _, _ := taskFixture(t)
	s.options.TaskHook = nil
	var original *builtinwrite.CallContext
	history := []queryloop.Message{{Record: input(`{"type":"user","uuid":"actual-user","message":{"role":"user","content":"actual context"}}`)}}
	seen := []string{}
	s.options.TaskHookWithContext = func(_ context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject, call queryloop.JSONToolCallContext) error {
		if !scope.BelongsTo(owner) || call.Opaque != original || string(call.ToolUseID) != `"actual-task-call"` || len(call.Messages) != 1 || call.Messages[0].Record.String("uuid") != "actual-user" {
			return fmt.Errorf("actual original call context replaced %#v", call)
		}
		row, err := s.options.TaskStore.Get("tasks", task.Get("id"))
		if err != nil || row == nil {
			return fmt.Errorf("hook preceded physical task %v", err)
		}
		seen = append(seen, event)
		return nil
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original = &builtinwrite.CallContext{Scope: scope, MainLoopModel: "actual-model", Messages: history}
		for _, row := range []struct{ name, body string }{{"TaskCreate", `{"subject":"actual created","description":"physical context"}`}, {"TaskUpdate", `{"taskId":"1","status":"completed"}`}} {
			future, err := s.startTask(context.Background(), row.name, input(row.body), queryloop.JSONToolCallContext{Opaque: original, ToolUseID: quote("actual-task-call"), Messages: history})
			if err != nil {
				return err
			}
			if _, err = future.Await(scope); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "TaskCreated" || seen[1] != "TaskCompleted" {
		t.Fatal("actual hook order", seen)
	}
}
