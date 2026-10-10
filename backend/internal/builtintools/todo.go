package builtintools

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const todoSuccess = "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable"

func (s *Service) todoEnabled() bool {
	if s.options.GetTodos == nil || s.options.SetTodos == nil || !s.options.NonInteractive {
		return false
	}
	switch strings.ToLower(strings.Trim(s.options.Environment["CLAUDE_CODE_ENABLE_TASKS"], "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")) {
	case "1", "true", "yes", "on":
		return false
	}
	return true
}

func (s *Service) todoWrite(scope *cooperative.Scope, in protocol.JSONObject) (json.RawMessage, error) {
	if !s.todoEnabled() {
		return nil, capability("TodoWrite actual AppState and noninteractive task profile")
	}
	oldTodos, err := s.options.GetTodos(scope)
	if err != nil {
		return nil, err
	}
	var todos []protocol.JSONObject
	if err := json.Unmarshal(in.Get("todos"), &todos); err != nil || todos == nil {
		return nil, capability("TodoWrite original parsed todo array")
	}
	allDone := true
	for _, todo := range todos {
		allDone = allDone && todo.String("status") == "completed"
	}
	updated := in.Get("todos")
	if allDone {
		updated = json.RawMessage("[]")
	}
	if err := s.options.SetTodos(scope, updated); err != nil {
		return nil, err
	}
	return object(protocol.JSONField{Name: "oldTodos", Value: oldTodos}, protocol.JSONField{Name: "newTodos", Value: in.Get("todos")}, field("verificationNudgeNeeded", false)), nil
}
