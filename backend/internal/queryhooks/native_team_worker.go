package queryhooks

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Main-session frontmatter hooks keep Stop on the real SessionID. Ordinary
// Agent registration retains its separate Stop → SubagentStop conversion.
func (s *Service) RegisterNativeMainHooks(sid string, hooks protocol.JSONObject) error {
	if sid == "" || !hooks.IsObject() {
		return errors.New("native main hooks require actual session identity and declaration")
	}
	store := s.NativeCommandStore()
	if store == nil {
		return unsupported("actual native command hook bindings")
	}
	prepared := map[string][]commandMatcher{}
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged"} {
		value := hooks.Get(event)
		if value == nil || string(value) == "null" {
			continue
		}
		var matchers []protocol.JSONObject
		if err := json.Unmarshal(value, &matchers); err != nil {
			return err
		}
		for _, matcher := range matchers {
			var commands []protocol.JSONObject
			if value := matcher.Get("hooks"); value == nil || string(value) == "null" {
				continue
			} else if err := json.Unmarshal(value, &commands); err != nil {
				return err
			}
			for _, hook := range commands {
				prepared[event] = append(prepared[event], commandMatcher{matcher.String("matcher"), hook.Clone()})
			}
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events[sid] == nil {
		store.events[sid] = map[string][]commandMatcher{}
	}
	for event, commands := range prepared {
		for _, command := range commands {
			rows := store.events[sid][event]
			insert := len(rows)
			for i, previous := range rows {
				if previous.matcher == command.matcher {
					insert = i + 1
				}
			}
			rows = append(rows, commandMatcher{})
			copy(rows[insert+1:], rows[insert:])
			rows[insert] = command
			store.events[sid][event] = rows
		}
	}
	return nil
}

func (s *Service) NativeMainHookDeclarations(sid string) protocol.JSONObject {
	result := protocol.NewJSONObject()
	store := s.NativeCommandStore()
	if store == nil {
		return result
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged"} {
		matchers := []protocol.JSONObject{}
		for _, command := range store.events[sid][event] {
			if len(matchers) > 0 && matchers[len(matchers)-1].String("matcher") == command.matcher {
				var hooks []protocol.JSONObject
				_ = json.Unmarshal(matchers[len(matchers)-1].Get("hooks"), &hooks)
				hooks = append(hooks, command.hook.Clone())
				matchers[len(matchers)-1].Set("hooks", commandJSON(hooks))
			} else {
				matchers = append(matchers, protocol.NewJSONObject(protocol.JSONField{Name: "matcher", Value: commandJSON(command.matcher)}, protocol.JSONField{Name: "hooks", Value: commandJSON([]protocol.JSONObject{command.hook})}))
			}
		}
		if len(matchers) > 0 {
			result.Set(event, commandJSON(matchers))
		}
	}
	return result
}
