package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeTaskHook(ctx context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject) error {
	return m.nativeTaskHookWithContext(ctx, scope, event, task, queryloop.JSONToolCallContext{})
}
func (m *MainPrint) nativeTaskHookWithContext(ctx context.Context, scope *cooperative.Scope, event string, task protocol.JSONObject, call queryloop.JSONToolCallContext) error {
	if !scope.BelongsTo(m.Owner) || m.hook == nil || !m.hook.HasNativeCommandBindings(m.Owner) {
		return cooperative.ErrRestricted
	}
	if call.Opaque != nil {
		if value, ok := call.Opaque.(*builtinwrite.CallContext); !ok || value == nil {
			return cooperative.ErrRestricted
		}
	}
	input := queryhooks.NativeTaskHookInput{SessionID: m.Controller.SessionID(), TaskID: task.Get("id"), TaskSubject: task.Get("subject"), TaskDescription: task.Get("description"), Call: queryhooks.Context{Scope: scope, ToolUseContext: call.Opaque, History: call.Messages, QuerySource: m.options.QuerySource}}
	if m.teamWorker != nil {
		input.TeammateName = quoteMain(m.teamWorker.preset.Start.Name)
		input.TeamName = quoteMain(m.teamWorker.preset.Start.TeamName)
		input.MainThreadAgentType = quoteMain(m.teamWorker.preset.Start.Name)
	} else {
		// Source registry lookup receives toolUseContext.agentId, while
		// createBaseHookInput(undefined) omits agent_id/permission_mode.
		input.Call.AgentID = m.options.AgentID
		if teammate := m.nativeInProcessTeammateIdentity(); teammate != nil {
			identity, err := m.Controller.TeammateIdentityOnOwner(scope)
			if err != nil || identity != *teammate {
				return cooperative.ErrRestricted
			}
			input.TeammateName, input.TeamName = quoteMain(identity.AgentName), quoteMain(identity.TeamName)
		}
	}
	blocked := []json.RawMessage{}
	consume := func(result queryhooks.EventResult) error {
		if value := result.Fields.Get("blockingError"); planRawTruthy(value) {
			var blocking protocol.JSONObject
			if err := blocking.UnmarshalJSON(value); err != nil {
				return err
			}
			feedback, err := queryhooks.FormatTaskHookFeedback(event, blocking)
			if err != nil {
				return err
			}
			blocked = append(blocked, feedback)
		}
		// Task tools consume blockingError only. Hook progress/system frames
		// are yielded by the real wrapper and deliberately not tool messages.
		return nil
	}
	var err error
	switch event {
	case "TaskCreated":
		err = m.hook.ExecuteTaskCreatedHooks(ctx, input, consume)
	case "TaskCompleted":
		err = m.hook.ExecuteTaskCompletedHooks(ctx, input, consume)
	default:
		return mainCap("actual task Hook event " + event)
	}
	if err != nil {
		return err
	}
	if len(blocked) > 0 {
		joined, err := protocol.JoinJavaScriptValuesJSON(blocked, "\n")
		if err != nil {
			return err
		}
		var text string
		if err = json.Unmarshal(joined, &text); err != nil {
			return err
		}
		return errors.New(text)
	}
	return nil
}
