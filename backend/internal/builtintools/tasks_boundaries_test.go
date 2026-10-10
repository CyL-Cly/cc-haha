package builtintools

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func taskFixture(t *testing.T) (*Service, *cooperative.Owner, *protocol.JSONObject, *string) {
	t.Helper()
	s, owner, home, permission := fixture(t)
	store, e := taskstore.New(taskstore.Options{ConfigDir: filepath.Join(home, ".claude"), PID: os.Getpid(), Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	s.options.TaskStore = store
	s.options.NonInteractive = false
	s.options.TaskListID = func(scope *cooperative.Scope, _ string) (string, error) {
		if !scope.BelongsTo(owner) {
			return "", cooperative.ErrRestricted
		}
		return "tasks", nil
	}
	view := "none"
	s.options.ExpandTaskView = func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		view = "tasks"
		return nil
	}
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { return nil }
	return s, owner, permission, &view
}
func invokeTask(t *testing.T, s *Service, owner *cooperative.Owner, name string, in protocol.JSONObject) (jsonResult queryloop.ToolCallResult, failure error) {
	t.Helper()
	failure = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, e := s.startTask(context.Background(), name, in, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if e != nil {
			return e
		}
		jsonResult, e = future.Await(scope)
		return e
	})
	return
}
func TestTaskCreatedBlockingHookRollsBackActualFileAndID(t *testing.T) {
	s, owner, _, view := taskFixture(t)
	seen := false
	s.options.TaskHook = func(_ context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject) error {
		if !scope.BelongsTo(owner) || event != "TaskCreated" || task.String("id") != "1" {
			t.Fatal("wrong original hook context")
		}
		existing, e := s.options.TaskStore.Get("tasks", quote("1"))
		if e != nil || existing == nil {
			t.Fatal("hook preceded creation")
		}
		seen = true
		return errors.New("TaskCreated hook feedback:\nblocked")
	}
	_, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"blocked","description":"details"}`))
	if e == nil || e.Error() != "TaskCreated hook feedback:\nblocked" || !seen {
		t.Fatalf("hook feedback %v", e)
	}
	snapshot, e := s.options.TaskStore.Snapshot("tasks")
	if e != nil || len(snapshot.Tasks) != 0 || snapshot.Revision != 2 || *view != "none" {
		t.Fatalf("failed creation was not rolled back %#v %v %s", snapshot, e, *view)
	}
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { return nil }
	result, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"later","description":""}`))
	if e != nil {
		t.Fatal(e)
	}
	var task protocol.JSONObject
	_ = task.UnmarshalJSON(input(string(result.Data)).Get("task"))
	if task.String("id") != "2" || *view != "tasks" {
		t.Fatalf("rolled-back ID was reused %s %s", result.Data, *view)
	}
}
func TestTaskCompletionBlockingHookReturnsBenignFailureWithoutCommit(t *testing.T) {
	s, owner, _, _ := taskFixture(t)
	if _, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"pending","description":"details"}`)); e != nil {
		t.Fatal(e)
	}
	s.options.TaskHook = func(_ context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject) error {
		if !scope.BelongsTo(owner) || event != "TaskCompleted" || task.String("status") != "pending" {
			t.Fatal("wrong completion hook")
		}
		return errors.New("TaskCompleted hook feedback:\nretry")
	}
	result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","status":"completed","metadata":{"ignored":true}}`))
	if e != nil {
		t.Fatal(e)
	}
	mapped, e := MapResult("TaskUpdate", result.Data, quote("id"))
	if e != nil || mapped.Get("is_error") != nil || mapped.String("content") != "TaskCompleted hook feedback:\nretry" {
		t.Fatalf("benign Source completion feedback %s %v", raw(mapped), e)
	}
	snapshot, e := s.options.TaskStore.Snapshot("tasks")
	if e != nil || snapshot.Revision != 1 || snapshot.Tasks[0].String("status") != "pending" || snapshot.Tasks[0].Get("metadata") != nil {
		t.Fatalf("blocked completion committed %#v %v", snapshot, e)
	}
}
func TestTaskCatalogPermissionAndConcurrencyUseActualEnablement(t *testing.T) {
	s, owner, permission, _ := taskFixture(t)
	permission.Set("mode", quote("default"))
	permission.Set("alwaysDenyRules", raw(map[string][]string{"projectSettings": {"Read(//**)", "Edit(//**)"}}))
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		definitions, tools, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		count := 0
		for _, tool := range tools {
			if !taskTool(tool.Name) {
				continue
			}
			count++
			want := tool.Name == "TaskCreate" || tool.Name == "TaskUpdate"
			if tool.IsConcurrencySafe(protocol.NewJSONObject()) != want || (tool.StartCallJSON != nil) != want {
				t.Fatalf("Source task concurrency changed %s", tool.Name)
			}
			behavior, e := s.Permission(scope, tool.Name, input(`{"taskId":"1"}`))
			if e != nil || behavior != "allow" {
				t.Fatalf("filesystem rules changed task permission %s %v", behavior, e)
			}
		}
		if count != 4 {
			t.Fatalf("actual task catalog %d, defs %d", count, len(definitions))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	s.options.NonInteractive = true
	s.options.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "off"
	if s.taskEnabled() {
		t.Fatal("headless task profile active without original env")
	}
	s.options.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "\ufeffYES "
	if !s.taskEnabled() {
		t.Fatal("Source truthy environment inactive")
	}
	permission.Set("alwaysDenyRules", raw(map[string][]string{"userSettings": {"TaskGet"}}))
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		decision, e := s.PermissionDecision(scope, "TaskGet", ptrObject(input(`{"taskId":"1"}`)))
		if e != nil {
			return e
		}
		if decision.Behavior != "deny" || string(decision.Message) != string(quote("Permission to use TaskGet has been denied.")) {
			t.Fatalf("task deny %s", decision.Message)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func ptrObject(object protocol.JSONObject) *protocol.JSONObject { return &object }
