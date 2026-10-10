package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"math"
)

func (m *MainPrint) appendNativeInProcessPrompt(ctx context.Context, taskID, prompt string) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		message := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(prompt)}))
		return struct{}{}, m.Controller.UpdateBackgroundTaskOnOwner(scope, taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			return teamspawn.AppendMessage(task, message)
		})
	})
	return err
}

func (m *MainPrint) newNativeInProcessProgress(ctx context.Context) (*foregroundAgentProgress, error) {
	return inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (*foregroundAgentProgress, error) {
		catalogue, err := m.Host.NativeToolSearchCatalogue(scope)
		if err != nil {
			return nil, err
		}
		if catalogue == nil {
			return nil, mainCap("in-process original progress tool pool")
		}
		names := make([]string, 0, len(catalogue.Tools))
		for _, tool := range catalogue.Tools {
			var name string
			_ = json.Unmarshal(tool.Name, &name)
			names = append(names, name)
		}
		return m.nativeAgentProgress(builtintools.AgentRequest{ParentTools: names}, m.workingDirectory()), nil
	})
}

func (m *MainPrint) observeNativeInProcessMessage(scope *cooperative.Scope, taskID string, progress *foregroundAgentProgress, message protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || progress == nil {
		return cooperative.ErrRestricted
	}
	progress.observe(rawMain(message))
	if progress.failure != nil {
		return progress.failure
	}
	return m.Controller.UpdateBackgroundTaskOnOwner(scope, taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		return teamspawn.ObserveMessage(task, message, progress.snapshot())
	})
}

func nativeTeamStreamScope(team *protocol.JSONObject) string {
	if team == nil {
		return ""
	}
	var created float64
	_ = json.Unmarshal(team.Get("createdAt"), &created)
	if math.IsNaN(created) || math.IsInf(created, 0) {
		created = 0
	}
	leader := team.Get("leadSessionId")
	if leader == nil || string(leader) == "null" {
		leader = quoteMain("")
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(rawMain([]json.RawMessage{team.Get("name"), leader, rawMain(created)})))
}

func (m *MainPrint) emitNativeInProcessRun(scope *cooperative.Scope, id, stream, target, scopeID, kind string, data json.RawMessage, failure error) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("agent_run_message")}, protocol.JSONField{Name: "run_agent_id", Value: quoteMain(id)}, protocol.JSONField{Name: "stream_id", Value: quoteMain(stream)}, protocol.JSONField{Name: "target_agent_id", Value: quoteMain(target)}, protocol.JSONField{Name: "event_kind", Value: quoteMain(kind)})
	if scopeID != "" {
		event.Set("target_agent_scope_id", quoteMain(scopeID))
	}
	if data != nil {
		event.Set("message", data)
	}
	if failure != nil {
		event.Set("error", quoteMain(failure.Error()))
	}
	return m.nativeBackgroundBindings().Emit(scope, event)
}
