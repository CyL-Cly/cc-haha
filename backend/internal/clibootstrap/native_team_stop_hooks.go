package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (m *MainPrint) nativeTeammateStopBinding() func(context.Context, queryhooks.Context, queryloop.Emit) (queryloop.StopDecision, error) {
	if m.teamWorker == nil && m.nativeInProcessTeammateIdentity() == nil {
		return nil
	}
	return m.nativeTeammateStopHooks
}

func (m *MainPrint) nativeTeammateStopHooks(ctx context.Context, call queryhooks.Context, emit queryloop.Emit) (queryloop.StopDecision, error) {
	scope := call.Scope
	decision := queryloop.StopDecision{BlockingMessages: []queryloop.Message{}}
	if !scope.BelongsTo(m.Owner) || emit == nil || m.teamWorker == nil && m.nativeInProcessTeammateIdentity() == nil {
		return decision, cooperative.ErrRestricted
	}
	identity, err := m.Controller.TeammateIdentityOnOwner(scope)
	if err != nil {
		return decision, err
	}
	team, name, id := identity.TeamName, identity.AgentName, identity.AgentID
	var mainType json.RawMessage
	taskAgentID := m.options.AgentID
	if m.teamWorker != nil {
		if call.AgentID != "" || team != m.teamWorker.preset.Start.TeamName || name != m.teamWorker.preset.Start.Name || id != m.teamWorker.preset.AgentID {
			return decision, cooperative.ErrRestricted
		}
		mainType = quoteMain(m.teamWorker.preset.Start.Name)
	} else {
		if call.AgentID != taskAgentID || identity != *m.nativeInProcessTeammateIdentity() {
			return decision, cooperative.ErrRestricted
		}
	}
	list, err := m.Controller.TaskListIDOnOwner(scope, taskAgentID)
	if err != nil {
		return decision, err
	}
	read, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) { return m.taskStore.List(list) })
	if err != nil {
		return decision, err
	}
	tasks, err := read.Await(scope)
	if err != nil {
		return decision, err
	}
	permission, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return decision, err
	}
	input := queryhooks.NativeTaskHookInput{SessionID: m.Controller.SessionID(), TeammateName: quoteMain(name), TeamName: quoteMain(team), PermissionMode: permission.Get("mode"), MainThreadAgentType: mainType, Call: call}
	lastID := quoteMain("")
	aborted := errors.New("actual teammate Hook consumer aborted")
	consume := func(event string) func(queryhooks.EventResult) error {
		return func(frame queryhooks.EventResult) error {
			if frame.Message != nil {
				if frame.Message.Record.String("type") == "progress" && planRawTruthy(frame.Message.Record.Get("toolUseID")) {
					lastID = frame.Message.Record.Get("toolUseID")
				}
				if err := emit(queryloop.Yield{Message: frame.Message}); err != nil {
					return err
				}
			}
			if value := frame.Fields.Get("blockingError"); planRawTruthy(value) {
				var blocking protocol.JSONObject
				if err := blocking.UnmarshalJSON(value); err != nil {
					return err
				}
				feedback, err := queryhooks.FormatTaskHookFeedback(event, blocking)
				if err != nil {
					return err
				}
				message := queryloop.Message{Record: messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": feedback, "isMeta": []byte("true")}))}
				decision.BlockingMessages = append(decision.BlockingMessages, message)
				if err = emit(queryloop.Yield{Message: &message}); err != nil {
					return err
				}
			}
			if planRawTruthy(frame.Fields.Get("preventContinuation")) {
				decision.PreventContinuation = true
				reason := frame.Fields.Get("stopReason")
				if !planRawTruthy(reason) {
					reason = quoteMain(event + " hook prevented continuation")
				}
				attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("hook_stopped_continuation")}, protocol.JSONField{Name: "message", Value: reason}, protocol.JSONField{Name: "hookName", Value: quoteMain(event)}, protocol.JSONField{Name: "toolUseID", Value: lastID}, protocol.JSONField{Name: "hookEvent", Value: quoteMain(event)})
				message := queryloop.Message{Record: m.factory.CreateAttachment(rawMain(attachment))}
				if err := emit(queryloop.Yield{Message: &message}); err != nil {
					return err
				}
			}
			if ctx.Err() != nil {
				return aborted
			}
			return nil
		}
	}
	for _, task := range tasks {
		if task.String("status") != "in_progress" || task.String("owner") != name {
			continue
		}
		input.TaskID, input.TaskSubject, input.TaskDescription = task.Get("id"), task.Get("subject"), task.Get("description")
		if err = m.hook.ExecuteTaskCompletedHooks(ctx, input, consume("TaskCompleted")); err != nil {
			if errors.Is(err, aborted) {
				return queryloop.StopDecision{PreventContinuation: true, BlockingMessages: []queryloop.Message{}}, nil
			}
			return decision, err
		}
	}
	// executeTeammateIdleHooks does not forward toolUseContext to executeHooks.
	// Completed hooks above retain the actual Stop caller's context.
	input.Call.ToolUseContext = nil
	input.Call.AgentID = ""
	if err = m.hook.ExecuteTeammateIdleHooks(ctx, input, consume("TeammateIdle")); err != nil {
		if errors.Is(err, aborted) {
			return queryloop.StopDecision{PreventContinuation: true, BlockingMessages: []queryloop.Message{}}, nil
		}
		return decision, err
	}
	if decision.PreventContinuation {
		decision.BlockingMessages = []queryloop.Message{}
	} else if len(decision.BlockingMessages) > 0 {
		// This continuation comes from the actual owner-bound MAIN teammate
		// executor; the finite/no-producer query-loop guard stays unchanged.
		decision.AllowBlockingContinuation = true
	}
	if m.teamWorker != nil && m.teamWorker.pane != nil {
		history := []protocol.JSONObject{}
		for _, message := range call.History {
			history = append(history, message.Record)
		}
		if err := m.nativePaneIdleOnOwner(scope, history); err != nil {
			return decision, err
		}
	}
	return decision, nil
}
