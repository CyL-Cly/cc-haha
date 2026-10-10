package queryhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Task inputs come from the actual task/teammate caller. Nil optional fields
// retain Source undefined (omitted), independently of empty strings or null.
type NativeTaskHookInput struct {
	SessionID                              string
	TaskID, TaskSubject, TaskDescription   json.RawMessage
	TeammateName, TeamName, PermissionMode json.RawMessage
	MainThreadAgentType                    json.RawMessage
	Call                                   Context
}

func (s *Service) ExecuteTaskCreatedHooks(ctx context.Context, in NativeTaskHookInput, consume func(EventResult) error) error {
	return s.executeNativeTaskEvent(ctx, "TaskCreated", in, consume)
}
func (s *Service) ExecuteTaskCompletedHooks(ctx context.Context, in NativeTaskHookInput, consume func(EventResult) error) error {
	return s.executeNativeTaskEvent(ctx, "TaskCompleted", in, consume)
}
func (s *Service) ExecuteTeammateIdleHooks(ctx context.Context, in NativeTaskHookInput, consume func(EventResult) error) error {
	return s.executeNativeTaskEvent(ctx, "TeammateIdle", in, consume)
}
func (s *Service) executeNativeTaskEvent(ctx context.Context, event string, in NativeTaskHookInput, consume func(EventResult) error) error {
	if s == nil || consume == nil {
		return errors.New("task Hook event requires actual service and consumer")
	}
	fields := protocol.NewJSONObject()
	if event == "TeammateIdle" {
		// The original Idle wrapper has no toolUseContext parameter. Its prompt
		// hook branch therefore reports the original missing-context error.
		in.Call.ToolUseContext = nil
		in.Call.AgentID = ""
	}
	if event != "TeammateIdle" {
		fields.Set("task_id", in.TaskID)
		fields.Set("task_subject", in.TaskSubject)
		fields.Set("task_description", in.TaskDescription)
	}
	fields.Set("teammate_name", in.TeammateName)
	fields.Set("team_name", in.TeamName)
	// These wrappers call createBaseHookInput(permissionMode) without passing
	// toolUseContext as agentInfo, even when registry lookup uses agentId.
	fields.Set("permission_mode", in.PermissionMode)
	fields.Set("agent_id", nil)
	fields.Set("agent_type", in.MainThreadAgentType)
	sid := in.SessionID
	if in.Call.AgentID != "" {
		sid = in.Call.AgentID
	}
	if sid == "" {
		sid = s.mainSessionID()
	}
	id := s.options.UUID()
	return s.ExecuteCommandEvent(ctx, CommandEvent{Event: event, SessionID: sid, ToolUseID: id, Fields: fields, Call: in.Call}, func(value EventResult) error { return s.expandNativeTaskResult(event, id, value, consume) })
}

func (s *Service) expandNativeTaskResult(event, id string, value EventResult, consume func(EventResult) error, displayNames ...string) error {
	hookName := event
	if len(displayNames) > 0 {
		hookName = displayNames[0]
	}
	fields := value.Fields
	frame := func(f protocol.JSONObject) error {
		return consume(EventResult{Fields: f, Outcome: value.Outcome, Hook: value.Hook.Clone()})
	}
	if hookTruthy(fields.Get("preventContinuation")) {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "preventContinuation", Value: []byte("true")}, protocol.JSONField{Name: "blockingError", Value: fields.Get("blockingError")}, protocol.JSONField{Name: "stopReason", Value: fields.Get("stopReason")})); err != nil {
			return err
		}
	}
	if blocked := fields.Get("blockingError"); hookTruthy(blocked) {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: blocked})); err != nil {
			return err
		}
	}
	if value.Message != nil {
		if err := consume(EventResult{Message: value.Message, Outcome: value.Outcome, Hook: value.Hook.Clone()}); err != nil {
			return err
		}
	}
	if text := fields.Get("systemMessage"); hookTruthy(text) {
		attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_system_message")}, protocol.JSONField{Name: "content", Value: text}, protocol.JSONField{Name: "hookName", Value: commandJSON(hookName)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(id)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(event)})
		message := queryloop.Message{Record: s.options.Commands.Factory.CreateAttachment(commandJSON(attachment))}
		if err := consume(EventResult{Message: &message, Outcome: value.Outcome, Hook: value.Hook.Clone()}); err != nil {
			return err
		}
	}
	if text := fields.Get("additionalContext"); hookTruthy(text) {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "additionalContexts", Value: commandJSON([]json.RawMessage{text})})); err != nil {
			return err
		}
	}
	if text := fields.Get("initialUserMessage"); hookTruthy(text) {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "initialUserMessage", Value: text})); err != nil {
			return err
		}
	}
	if paths := fields.Get("watchPaths"); hookTruthy(paths) {
		var rows []json.RawMessage
		if json.Unmarshal(paths, &rows) == nil && len(rows) > 0 {
			if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "watchPaths", Value: paths})); err != nil {
				return err
			}
		}
	}
	if output := fields.Get("updatedMCPToolOutput"); hookTruthy(output) {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "updatedMCPToolOutput", Value: output})); err != nil {
			return err
		}
	}
	if behavior := fields.Get("permissionBehavior"); behavior != nil {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "permissionBehavior", Value: behavior}, protocol.JSONField{Name: "hookPermissionDecisionReason", Value: fields.Get("hookPermissionDecisionReason")}, protocol.JSONField{Name: "hookSource", Value: fields.Get("hookSource")}, protocol.JSONField{Name: "updatedInput", Value: fields.Get("updatedInput")})); err != nil {
			return err
		}
	}
	if updated := fields.Get("passthroughUpdatedInput"); updated != nil {
		if err := frame(protocol.NewJSONObject(protocol.JSONField{Name: "updatedInput", Value: updated})); err != nil {
			return err
		}
	}
	return nil
}

func FormatTaskHookFeedback(event string, blocking protocol.JSONObject) (json.RawMessage, error) {
	if event != "TaskCreated" && event != "TaskCompleted" && event != "TeammateIdle" {
		return nil, errors.New("unsupported task Hook feedback event")
	}
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{commandJSON(event + " hook feedback:\n"), blocking.Get("blockingError")}, "")
}
