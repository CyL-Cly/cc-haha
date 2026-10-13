package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"strings"
)

type nativeInProcessTemplate struct {
	Identity           sessioncontroller.InProcessTeammateIdentity
	Definition         *agentcatalog.Definition
	Model, Description string
	TeamCreatedAt      json.RawMessage
}
type nativeInProcessResumeState struct {
	Templates map[string]nativeInProcessTemplate
	Resuming  map[string]*ownerasync.OwnerFuture[struct{}]
}
type nativeInProcessResume struct {
	Template nativeInProcessTemplate
	History  []protocol.JSONObject
	Metadata protocol.JSONObject
	From     string
}

func (m *MainPrint) rememberNativeInProcessTemplate(scope *cooperative.Scope, identity sessioncontroller.InProcessTeammateIdentity, definition *agentcatalog.Definition, model, description string) {
	if !scope.BelongsTo(m.Owner) {
		return
	}
	if m.nativeInProcess == nil {
		m.nativeInProcess = &nativeInProcessResumeState{Templates: map[string]nativeInProcessTemplate{}, Resuming: map[string]*ownerasync.OwnerFuture[struct{}]{}}
	}
	var created json.RawMessage
	if m.teamLeader != nil {
		if file := m.teamLeader.config.Read(identity.TeamName); file != nil {
			created = append(json.RawMessage(nil), file.Get("createdAt")...)
		}
	}
	m.nativeInProcess.Templates[identity.AgentID] = nativeInProcessTemplate{identity, definition, model, description, created}
}

func nativeResumableTeammatePermissionMode(metadata protocol.JSONObject) string {
	if metadata.String("taskKind") != "in_process_teammate" {
		return ""
	}
	switch mode := metadata.String("permissionMode"); mode {
	case "default", "acceptEdits", "plan", "dontAsk", "auto":
		return mode
	}
	return ""
}
func nativeInProcessMetadata(identity sessioncontroller.InProcessTeammateIdentity, definition *agentcatalog.Definition, model, mode string) protocol.JSONObject {
	fields := map[string]any{"agentType": identity.AgentName, "taskKind": "in_process_teammate", "teamName": identity.TeamName, "name": identity.AgentName, "planModeRequired": identity.PlanModeRequired, "permissionMode": mode}
	if model != "" {
		fields["model"] = model
	}
	if identity.Color != "" {
		fields["color"] = identity.Color
	}
	if definition != nil {
		fields["customAgentType"] = definition.Name
	}
	return nativeInProcessObject(rawMain(fields))
}
func (m *MainPrint) writeNativeInProcessMetadata(path string, metadata protocol.JSONObject) {
	metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	// Preserve old metadata fields when adding teammate restoration fields.
	if data, err := m.options.Native.readFile(metaPath); err == nil {
		if previous := nativeInProcessObject(data); previous.IsObject() {
			previous.Assign(metadata)
			metadata = previous
		}
	}
	m.writeAgentWorktreeMetadata(path, metadata)
}

// The owner's cooperative transaction deduplicates concurrent resume callers:
// registration occurs before this function yields its spawned task result.
func (m *MainPrint) resumeNativeInProcessTeammate(scope *cooperative.Scope, ctx context.Context, team, name, prompt, from, summary string) (returned *ownerasync.OwnerFuture[queryloop.ToolCallResult], handled bool, returnedErr error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil {
		return nil, false, nil
	}
	id := name + "@" + team
	if m.nativeInProcess != nil && m.nativeInProcess.Resuming[id] != nil {
		_, err := m.nativeInProcess.Resuming[id].Await(scope)
		if err != nil {
			return m.resumeNativeInProcessTeammate(scope, ctx, team, name, prompt, from, summary)
		}
		return nil, false, nil
	}
	tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return nil, false, err
	}
	for _, task := range tasks {
		identity, _ := task.Object("identity")
		if task.String("type") == "in_process_teammate" && identity.String("agentId") == id && task.String("status") == "running" {
			return nil, false, nil
		}
	}
	file := m.teamLeader.config.Read(team)
	if file == nil {
		return nil, false, nil
	}
	var members []protocol.JSONObject
	if err = json.Unmarshal(file.Get("members"), &members); err != nil {
		return nil, false, err
	}
	var member *protocol.JSONObject
	for _, row := range members {
		if row.String("name") == name {
			copy := row.Clone()
			member = &copy
			break
		}
	}
	var template nativeInProcessTemplate
	warm := false
	if m.nativeInProcess != nil {
		template, warm = m.nativeInProcess.Templates[id]
		warm = warm && string(template.TeamCreatedAt) == string(file.Get("createdAt"))
	}
	if !warm && (member == nil || member.String("backendType") != "in-process") {
		return nil, false, nil
	}
	if !warm {
		resumable, err := nativeAgentID()
		if err != nil {
			return nil, false, err
		}
		template = nativeInProcessTemplate{Identity: sessioncontroller.InProcessTeammateIdentity{AgentID: id, AgentName: name, TeamName: team, ParentSessionID: m.Controller.SessionID(), ResumableAgentID: resumable, Color: member.String("color"), PlanModeRequired: string(member.Get("planModeRequired")) == "true"}, Model: member.String("model"), TeamCreatedAt: append(json.RawMessage(nil), file.Get("createdAt")...)}
		definitions, err := m.agents.List(scope)
		if err != nil {
			return nil, false, err
		}
		for _, definition := range definitions.Active {
			if definition.Name == member.String("agentType") && definition.Source != "built-in" {
				copy := definition
				template.Definition = &copy
				break
			}
		}
	}
	if m.nativeInProcess == nil {
		m.nativeInProcess = &nativeInProcessResumeState{Templates: map[string]nativeInProcessTemplate{}, Resuming: map[string]*ownerasync.OwnerFuture[struct{}]{}}
	}
	if m.nativeInProcess.Resuming == nil {
		m.nativeInProcess.Resuming = map[string]*ownerasync.OwnerFuture[struct{}]{}
	}
	pending, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return nil, true, err
	}
	m.nativeInProcess.Resuming[id] = pending
	defer func() {
		delete(m.nativeInProcess.Resuming, id)
		if returnedErr != nil {
			_ = pending.Reject(scope, returnedErr)
		} else {
			_ = pending.Resolve(scope, struct{}{})
		}
	}()
	resume := &nativeInProcessResume{Template: template, Metadata: protocol.NewJSONObject(), History: []protocol.JSONObject{}, From: from}
	if warm {
		path, loader, reducer, err := m.Controller.AgentTranscriptSourceOnOwner(scope, template.Identity.ResumableAgentID)
		if err != nil {
			return nil, true, err
		}
		load, err := nativeSkillWork(m, scope, func() (nativeInProcessResume, error) {
			out := *resume
			if transcript := loader.LoadAgentTranscript(path, template.Identity.ResumableAgentID); transcript != nil {
				history, loadErr := reducer.FilterSubagent(transcript.Messages)
				if loadErr != nil {
					return out, loadErr
				}
				out.History = history
			}
			if data, err := m.options.Native.readFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
				_ = out.Metadata.UnmarshalJSON(data)
			}
			return out, nil
		})
		if err != nil {
			return nil, true, err
		}
		loaded, err := load.Await(scope)
		if err != nil {
			return nil, true, err
		}
		resume = &loaded
	}
	input := nativeInProcessObject(rawMain(map[string]any{"name": name, "prompt": prompt, "description": summary, "model": template.Model}))
	if template.Definition != nil {
		input.Set("subagent_type", quoteMain(template.Definition.Name))
	}
	future, err := m.spawnNativeInProcessTeamAgentFromCheckpoint(ctx, scope, team, input, queryloop.JSONToolCallContext{}, resume)
	if err != nil {
		return nil, true, err
	}
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, true, err
	}
	_, err = future.Then(scope, func(next *cooperative.Scope, result queryloop.ToolCallResult, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		message := fmt.Sprintf("Teammate %q was not running; resumed it as an in-process teammate (no prior transcript) with your message as its next prompt.", name)
		if len(resume.History) > 0 {
			message = fmt.Sprintf("Teammate %q was not running; resumed it as an in-process teammate with %d prior messages and your message as its next prompt.", name, len(resume.History))
		}
		response := nativeInProcessObject(rawMain(map[string]any{"success": true, "message": message}))
		return out.Resolve(next, queryloop.ToolCallResult{Data: rawMain(response)})
	})
	return out, true, err
}

func nativeInProcessObject(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}

// In-process teammates use their existing owner bridge to resume a peer in the
// lead's graph. Standalone process/pane workers remain mailbox-only, as MAIN.
func (m *MainPrint) tryResumeNativeInProcessTarget(scope *cooperative.Scope, ctx context.Context, in protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], bool, error) {
	if in.String("to") == "*" || m.teamWorker != nil {
		return nil, false, nil
	}
	sender := "team-lead"
	root := m
	if identity := m.nativeInProcessTeammateIdentity(); identity != nil {
		sender = identity.AgentName
		if m.options.DetachedAgent == nil || m.options.DetachedAgent.authParent == nil {
			return nil, false, nil
		}
		root = m.options.DetachedAgent.authParent
		for root.options.DetachedAgent != nil && root.options.DetachedAgent.authParent != nil {
			root = root.options.DetachedAgent.authParent
		}
	}
	team := m.nativeLeaderTeamName()
	if root == m {
		return m.resumeNativeInProcessTeammate(scope, ctx, team, in.String("to"), in.String("message"), sender, in.String("summary"))
	}
	type outcome struct {
		Handled bool
		Result  queryloop.ToolCallResult
	}
	resumed, err := nativeAgentOwnerCall(m, scope, root, func(next *cooperative.Scope) (outcome, error) {
		future, handled, err := root.resumeNativeInProcessTeammate(next, ctx, team, in.String("to"), in.String("message"), sender, in.String("summary"))
		if !handled || err != nil {
			return outcome{Handled: handled}, err
		}
		result, err := future.Await(next)
		return outcome{true, result}, err
	})
	if !resumed.Handled || err != nil {
		return nil, resumed.Handled, err
	}
	future, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, true, err
	}
	err = future.Resolve(scope, resumed.Result)
	return future, true, err
}
