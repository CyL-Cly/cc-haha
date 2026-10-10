package builtintools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativeTodoActualCallsAndAppStateTransitionsMatchSource(t *testing.T) {
	data, err := os.ReadFile("../toolschema/testdata/native_todo_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Observations []struct {
			Step struct {
				Name, Operation, InputSource, AgentID, TaskEnvironment string
				Interactive                                            bool
			}
			Observation struct {
				Enabled, Success                        bool
				ResultSource, MappedSource, StateSource string
			}
		}
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	s, owner, _, permissions := fixture(t)
	permissions.Set("mode", quote("default"))
	state := protocol.NewJSONObject(field("todos", protocol.NewJSONObject()), field("preservedOtherState", "actual-appstate"))
	key := "00000000-0000-4000-8000-000000000000"
	s.options.NonInteractive = true
	s.options.GetTodos = func(scope *cooperative.Scope) (json.RawMessage, error) {
		if !scope.BelongsTo(owner) {
			return nil, cooperative.ErrRestricted
		}
		var todos protocol.JSONObject
		_ = json.Unmarshal(state.Get("todos"), &todos)
		if value := todos.Get(key); value != nil {
			return value, nil
		}
		return json.RawMessage("[]"), nil
	}
	s.options.SetTodos = func(scope *cooperative.Scope, value json.RawMessage) error {
		if !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		var todos protocol.JSONObject
		_ = json.Unmarshal(state.Get("todos"), &todos)
		todos.Set(key, value)
		state.Set("todos", raw(todos))
		return nil
	}
	for _, row := range corpus.Observations {
		if row.Step.Operation == "enabled" {
			s.options.NonInteractive = !row.Step.Interactive
			s.options.Environment["CLAUDE_CODE_ENABLE_TASKS"] = row.Step.TaskEnvironment
			if s.todoEnabled() != row.Observation.Enabled {
				t.Fatalf("Source task profile %s changed", row.Step.Name)
			}
			continue
		}
		if row.Step.Operation != "call" {
			continue
		}
		if row.Step.AgentID != "" {
			key = row.Step.AgentID
		} else {
			key = "00000000-0000-4000-8000-000000000000"
		}
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			_, catalog, err := s.Catalog(scope)
			if err != nil {
				return err
			}
			var tool *queryloop.RegisteredTool
			for i := range catalog {
				if catalog[i].Name == "TodoWrite" {
					tool = &catalog[i]
				}
			}
			if tool == nil || tool.IsConcurrencySafe(input(row.Step.InputSource)) {
				t.Fatal("TodoWrite actual mutable tool missing or marked readonly")
			}
			parsed, err := tool.ParseInput(input(row.Step.InputSource))
			if err != nil {
				return err
			}
			permission, err := s.Permission(scope, "TodoWrite", parsed)
			if err != nil || permission != "allow" {
				t.Fatalf("TodoWrite required filesystem permission: %s %v", permission, err)
			}
			result, err := tool.CallJSON(context.Background(), parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote("actual-todo-tool-use-id")})
			if err != nil {
				return err
			}
			mapped, err := tool.MapResultJSON(result.Data, quote("actual-todo-tool-use-id"))
			if err != nil {
				return err
			}
			for _, pair := range []struct {
				name      string
				got, want json.RawMessage
			}{{"result", result.Data, []byte(row.Observation.ResultSource)}, {"mapped", raw(mapped), []byte(row.Observation.MappedSource)}, {"actual AppState", raw(state), []byte(row.Observation.StateSource)}} {
				if got, want := protocol.StringifyValidatedJavaScriptJSON(pair.got), protocol.StringifyValidatedJavaScriptJSON(pair.want); string(got) != string(want) {
					t.Fatalf("%s %s Source changed: %s != %s", row.Step.Name, pair.name, got, want)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTodoCatalogRequiresRealStateAndRetainsDenyRule(t *testing.T) {
	s, owner, _, permissions := fixture(t)
	s.options.NonInteractive = true
	if s.todoEnabled() {
		t.Fatal("missing actual AppState admitted")
	}
	s.options.GetTodos = func(*cooperative.Scope) (json.RawMessage, error) { return json.RawMessage("[]"), nil }
	s.options.SetTodos = func(*cooperative.Scope, json.RawMessage) error { return nil }
	permissions.Set("alwaysDenyRules", raw(map[string][]string{"cliArg": {"TodoWrite"}}))
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		defs, tools, err := s.Catalog(scope)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.Name == "TodoWrite" {
				t.Fatal("deny rule exposed TodoWrite to model")
			}
		}
		for _, d := range defs {
			if d.Name == "TodoWrite" {
				t.Fatal("deny rule exposed TodoWrite schema")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.options.NonInteractive = false
	if _, err := s.todoWrite(nil, input(`{"todos":[]}`)); err == nil || !strings.Contains(err.Error(), "noninteractive") {
		t.Fatal("interactive TaskV2 replaced with TodoWrite")
	}
}
