package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTodoWriteUsesLiveSessionAndAgentKeysInOriginalAppState(t *testing.T) {
	c, state, owner := boundController(t)
	c.options.NativeTools = true
	c.options.AsyncPersistence = true
	c.options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	mainTodos := json.RawMessage(`[{"content":"main","status":"pending","activeForm":"working main"}]`)
	childTodos := json.RawMessage(`[{"content":"child","status":"in_progress","activeForm":"working child"}]`)
	var child *Controller
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		ready, err := c.BeginActivateFreshOwned(scope, FreshBareSDKProfile)
		if err != nil {
			return err
		}
		if _, err = ready.Await(scope); err != nil {
			return err
		}
		if err = c.SetTodosOnOwner(scope, mainTodos); err != nil {
			return err
		}
		childState, err := bootstrapstate.New(owner, state.SessionID(), state.Snapshot().CWD, func() (string, error) { return "unused", nil })
		if err != nil {
			return err
		}
		c.options.RecordBranchProfile = OwnedGitRecordProfile
		branch, err := NewOwnedGitRecordBranch(childState, actualOwnedGitService(t, childState, owner))
		if err != nil {
			return err
		}
		child, err = c.NewAgentChild(scope, childState, branch, "a1234", func(context.Context, []protocol.JSONObject) ([]json.RawMessage, error) {
			return []json.RawMessage{quote("child")}, nil
		})
		if err != nil {
			return err
		}
		if err = child.ActivateAgentOwned(scope); err != nil {
			return err
		}
		if key, err := child.TaskListIDOnOwner(scope, "a1234"); err != nil || key != "a1234" {
			t.Fatalf("child task identity %q %v", key, err)
		}
		if _, err := c.TaskListIDOnOwner(scope, "a1234"); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign agent task identity admitted", err)
		}
		if view, err := child.ExpandedViewOnOwner(scope); err != nil || view != "none" {
			t.Fatalf("default Source expanded view %q %v", view, err)
		}
		if err = child.ExpandTaskViewOnOwner(scope); err != nil {
			return err
		}
		if view, err := c.ExpandedViewOnOwner(scope); err != nil || view != "tasks" {
			t.Fatalf("child detached root Task view %q %v", view, err)
		}
		child.options.ResumeOptions.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "exact team override 😀"
		if key, err := child.TaskListIDOnOwner(scope, "a1234"); err != nil || key != "exact team override 😀" {
			t.Fatalf("env task list identity %q %v", key, err)
		}
		got, err := child.GetTodosOnOwner(scope)
		if err != nil || string(got) != "[]" {
			t.Fatalf("agent inherited root checklist: %s %v", got, err)
		}
		if err = child.SetTodosOnOwner(scope, childTodos); err != nil {
			return err
		}
		if string(c.appState.Todos.Get("a1234")) != string(childTodos) {
			t.Fatal("sidechain detached from process AppState")
		}
		got, err = c.GetTodosOnOwner(scope)
		if err != nil || string(got) != string(mainTodos) {
			t.Fatalf("agent changed main checklist: %s %v", got, err)
		}
		got[0] = 'x'
		if err = child.SetTodosOnOwner(scope, json.RawMessage("[]")); err != nil {
			return err
		}
		if string(c.appState.Todos.Get("a1234")) != "[]" || string(c.appState.Todos.Get(state.SessionID())) != string(mainTodos) {
			t.Fatal("completion cleared another key or returned mutable AppState bytes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if child != nil {
		child.AwaitBackground()
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { return c.SetTodosOnOwner(scope, json.RawMessage("[]")) }); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign owner wrote process AppState", err)
	}
	if _, err := c.GetTodosOnOwner(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale/nil scope read AppState", err)
	}
}
