package builtintools

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"testing"
)

func TestTeammateTaskGuardOwnerAndCompletionNudge(t *testing.T) {
	s, owner, _, _ := taskFixture(t)
	s.options.Team = &TeamBindings{IsTeammate: true, AgentName: func(*cooperative.Scope) (string, error) { return "writer", nil }}
	create := func(subject string) {
		t.Helper()
		if _, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"`+subject+`","description":"details"}`)); e != nil {
			t.Fatal(e)
		}
	}
	create("upstream")
	create("downstream")
	if _, e := s.options.TaskStore.Update("tasks", quote("2"), input(`{"addBlockedBy":["1","missing"]}`)); e != nil {
		t.Fatal(e)
	}
	hooks := 0
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { hooks++; return nil }
	for _, status := range []string{"in_progress", "completed"} {
		result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"2","status":"`+status+`"}`))
		if e != nil {
			t.Fatal(e)
		}
		data := input(string(result.Data))
		if data.String("error") != "Task #2 waits on #1, not completed yet. Do not work on it or change its status until then; TaskList shows which of your tasks are ready." || hooks != 0 {
			t.Fatalf("guard %s hooks %d", result.Data, hooks)
		}
	}
	// The coordinating lead can deliberately override the dependency.
	s.options.Team.IsTeammate = false
	if result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"2","status":"in_progress"}`)); e != nil || !boolean(input(string(result.Data)), "success", false) {
		t.Fatalf("lead %s %v", result.Data, e)
	}
	s.options.Team.IsTeammate = true
	if _, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","status":"completed"}`)); e != nil {
		t.Fatal(e)
	}
	result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"2","status":"completed"}`))
	if e != nil {
		t.Fatal(e)
	}
	task, _ := s.options.TaskStore.Get("tasks", quote("2"))
	if task.String("owner") != "writer" {
		t.Fatalf("owner %s", raw(task))
	}
	var mapped protocol.JSONObject
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		for _, tool := range tools {
			if tool.Name == "TaskUpdate" {
				mapped, e = tool.MapResultJSON(result.Data, quote("actual-id"))
				return e
			}
		}
		t.Fatal("missing TaskUpdate")
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(mapped.String("content"), "Task completed. Call TaskList now to find your next available task or see if your work unblocked others.") {
		t.Fatalf("completion prompt %s", raw(mapped))
	}
}

func TestTeammateTaskRechecksAfterHookAndReviewPrecedesHook(t *testing.T) {
	s, owner, _, _ := taskFixture(t)
	for _, subject := range []string{"one", "two"} {
		if _, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"`+subject+`","description":""}`)); e != nil {
			t.Fatal(e)
		}
	}
	s.options.Team = &TeamBindings{IsTeammate: true, AgentName: func(*cooperative.Scope) (string, error) { return "writer", nil }, TeamName: func(*cooperative.Scope) (string, error) { return "reviewed", nil }, TaskExecutionApproved: func(string, string) (bool, error) { return false, nil }}
	hooks := 0
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { hooks++; return nil }
	result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"2","status":"completed"}`))
	if e != nil || !strings.Contains(input(string(result.Data)).String("error"), "awaiting human review") || hooks != 0 {
		t.Fatalf("review %s %v hooks %d", result.Data, e, hooks)
	}
	s.options.Team.TaskExecutionApproved = func(string, string) (bool, error) { return true, nil }
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error {
		hooks++
		_, e := s.options.TaskStore.Update("tasks", quote("2"), input(`{"addBlockedBy":["1"]}`))
		return e
	}
	result, e = invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"2","status":"completed"}`))
	if e != nil || !strings.Contains(input(string(result.Data)).String("error"), "waits on #1") || hooks != 1 {
		t.Fatalf("atomic guard %s %v hooks %d", result.Data, e, hooks)
	}
	task, _ := s.options.TaskStore.Get("tasks", quote("2"))
	if task.String("status") != "pending" {
		t.Fatalf("blocked committed %s", raw(task))
	}
	// Owner changes produce one assignment, while completed work never wakes a new owner.
	assignments := []json.RawMessage{}
	s.options.Team.IsTeammate = false
	s.options.Team.TaskAssigned = func(_ string, task protocol.JSONObject) error {
		assignments = append(assignments, raw(task))
		return nil
	}
	if _, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","owner":"reviewer"}`)); e != nil {
		t.Fatal(e)
	}
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { return nil }
	if _, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","status":"completed","owner":"other"}`)); e != nil {
		t.Fatal(e)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignment count %d", len(assignments))
	}
}
