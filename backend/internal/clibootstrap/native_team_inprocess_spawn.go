package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"os"
	"path/filepath"
	"strings"
)

func (m *MainPrint) nativeTeammateColor(scope *cooperative.Scope, id string) (string, error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil {
		return "", cooperative.ErrRestricted
	}
	leader := m.teamLeader
	if color := leader.colors[id]; color != "" {
		return color, nil
	}
	colors := []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}
	color := colors[leader.colorIndex%len(colors)]
	leader.colorIndex++
	leader.colors[id] = color
	return color, nil
}

// This producer performs the original in-process branch only. Pane mode is
// admitted by the native backend selector separately, never silently replaced.
func (m *MainPrint) spawnNativeInProcessTeamAgent(ctx context.Context, scope *cooperative.Scope, team string, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil || m.nativeInProcessTeammateIdentity() != nil || m.teamWorker != nil {
		return nil, cooperative.ErrRestricted
	}
	if input.String("name") == "" || input.String("prompt") == "" {
		return nil, errors.New("name and prompt are required for spawn operation")
	}
	if team == "" {
		return nil, errors.New("team_name is required for spawn operation. Either provide team_name in input or call spawnTeam first to establish team context.")
	}
	leaderModel, err := m.nativeTeamLeaderModel(scope)
	if err != nil {
		return nil, err
	}
	definitions, err := m.agents.List(scope)
	if err != nil {
		return nil, err
	}
	var selected *agentcatalog.Definition
	for _, definition := range definitions.Active {
		if definition.Name == input.String("subagent_type") {
			copy := definition
			selected = &copy
			break
		}
	}
	agentModel := ""
	if selected != nil {
		agentModel = selected.Model
	}
	global, err := m.Global.Get()
	if err != nil {
		return nil, err
	}
	model, err := teamspawn.ResolveModel(m.options.Native.Environment["CLAUDE_CODE_SUBAGENT_MODEL"], input.String("model"), agentModel, leaderModel, selected != nil, global.Get("teammateDefaultModel"), m.nativeTeamParsedModel, func() (string, error) { return teamspawn.HardcodedModelFallback(m.options.Native.Environment), nil })
	if err != nil {
		return nil, err
	}
	var preset *agentcatalog.Definition
	if selected != nil && selected.Source != "built-in" {
		preset = selected
	}
	read, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
		file := m.teamLeader.config.Read(team)
		if file == nil {
			return nil, nil
		}
		var members []protocol.JSONObject
		err := json.Unmarshal(file.Get("members"), &members)
		return members, err
	})
	if err != nil {
		return nil, err
	}
	members, err := read.Await(scope)
	if err != nil {
		return nil, err
	}
	name := strings.ReplaceAll(teamspawn.UniqueName(input.String("name"), members), "@", "-")
	id := name + "@" + team
	color, err := m.nativeTeammateColor(scope, id)
	if err != nil {
		return nil, err
	}
	taskID, err := teamspawn.GenerateTaskID(rand.Reader)
	if err != nil {
		return nil, err
	}
	directory, err := m.nativeTaskOutputDirectory(scope)
	if err != nil {
		return nil, err
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	verbs, err := teamspawn.SpinnerVerbs(*settings)
	if err != nil {
		return nil, err
	}
	spinner, err := teamspawn.Sample(verbs)
	if err != nil {
		return nil, err
	}
	past, err := teamspawn.Sample(teamspawn.CompletionVerbs)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, taskID+".output")
	data, err := teamspawn.CreateState(teamspawn.StateOptions{ID: taskID, Name: name, Team: team, Prompt: input.String("prompt"), ParentSessionID: m.Controller.SessionID(), Color: color, OutputFile: path, Model: quoteMain(model), PlanRequired: input.String("mode") == "plan", ToolUseID: call.ToolUseID, SpinnerVerb: spinner, PastTenseVerb: past, Now: m.options.Native.NowMillis()})
	if err != nil {
		return nil, err
	}
	identity := sessioncontroller.InProcessTeammateIdentity{TeamName: team, AgentName: name, AgentID: id, ParentSessionID: m.Controller.SessionID(), Color: color, PlanModeRequired: input.String("mode") == "plan"}
	life, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})
	join := func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	unregister, err := m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
		cancel(context.Canceled)
		return nativeSkillWork(m, next, func() (struct{}, error) { return struct{}{}, join(context.Background()) })
	}))
	if err != nil {
		cancel(err)
		return nil, err
	}
	copyBinding, err := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if err != nil {
		_, _ = unregister(scope)
		cancel(err)
		return nil, err
	}
	ref, err := copyBinding.Acquire(scope)
	if err != nil {
		_, _ = unregister(scope)
		cancel(err)
		return nil, err
	}
	entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return foregroundTaskRead(path) }, Join: join}
	entry.Stop = func(next *cooperative.Scope, now float64) error {
		return m.killNativeTeammateOnOwner(next, taskID, now, unregister)
	}
	if err = m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
		ref.DeliveryFinished()
		_, _ = unregister(scope)
		cancel(err)
		return nil, err
	}
	started := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_started")}, protocol.JSONField{Name: "task_id", Value: quoteMain(taskID)}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID}, protocol.JSONField{Name: "description", Value: data.Get("description")}, protocol.JSONField{Name: "task_type", Value: quoteMain("in_process_teammate")}, protocol.JSONField{Name: "prompt", Value: input.Get("prompt")}, protocol.JSONField{Name: "owner_agent_id", Value: quoteMain(id)})
	if err = m.nativeBackgroundBindings().Emit(scope, started); err != nil {
		ref.DeliveryFinished()
		_, _ = unregister(scope)
		cancel(err)
		_ = m.Controller.DeleteBackgroundTaskOnOwner(scope, taskID)
		return nil, err
	}
	go func() {
		defer close(done)
		defer ref.DeliveryFinished()
		failure := m.runNativeInProcessLoop(life, taskID, identity, preset, model, input.String("prompt"), input.String("description"))
		if err := m.finishNativeInProcessTask(context.Background(), taskID, identity, path, failure, unregister); err != nil {
			_, _ = fmt.Fprintln(m.options.Error, err)
		}
	}()
	// Source registers AppState roster after starting the fire-and-forget loop.
	snapshot := protocol.NewJSONObject()
	if m.teamState != nil {
		snapshot = m.teamState.snapshot.Clone()
	}
	teammates, ok := snapshot.Object("teammates")
	if !ok {
		teammates = protocol.NewJSONObject()
	} else {
		teammates = teammates.Clone()
	}
	leadID := snapshot.String("leadAgentId")
	if leadID == "" {
		leadID = "team-lead@" + team
		leadColor, err := m.nativeTeammateColor(scope, leadID)
		if err != nil {
			return nil, err
		}
		teammates.Set(leadID, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "agentType", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "color", Value: quoteMain(leadColor)}, protocol.JSONField{Name: "tmuxSessionName", Value: quoteMain("in-process")}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain("leader")}, protocol.JSONField{Name: "cwd", Value: quoteMain(m.workingDirectory())}, protocol.JSONField{Name: "spawnedAt", Value: rawMain(m.options.Native.NowMillis())})))
	}
	snapshot.Set("teamName", quoteMain(team))
	if snapshot.Get("teamFilePath") == nil {
		snapshot.Set("teamFilePath", quoteMain(""))
	}
	snapshot.Set("leadAgentId", quoteMain(leadID))
	teammates.Set(id, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "agentType", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "tmuxSessionName", Value: quoteMain("in-process")}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain("in-process")}, protocol.JSONField{Name: "cwd", Value: quoteMain(m.workingDirectory())}, protocol.JSONField{Name: "spawnedAt", Value: rawMain(m.options.Native.NowMillis())})))
	snapshot.Set("teammates", rawMain(teammates))
	m.teamState = &nativeTeamState{snapshot: snapshot}
	if err = m.Controller.SetLeaderTeamOnOwner(scope, team); err != nil {
		return nil, err
	}
	member := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoteMain(id)}, protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "agentType", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "prompt", Value: input.Get("prompt")}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "planModeRequired", Value: rawMain(identity.PlanModeRequired)}, protocol.JSONField{Name: "joinedAt", Value: rawMain(m.options.Native.NowMillis())}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain("in-process")}, protocol.JSONField{Name: "cwd", Value: quoteMain(m.workingDirectory())}, protocol.JSONField{Name: "subscriptions", Value: rawMain([]string{})}, protocol.JSONField{Name: "backendType", Value: quoteMain("in-process")})
	write, err := nativeSkillWork(m, scope, func() (queryloop.ToolCallResult, error) {
		_, err := m.teamLeader.config.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
			var members []protocol.JSONObject
			if err := json.Unmarshal(file.Get("members"), &members); err != nil {
				return file, err
			}
			file.Set("members", rawMain(append(members, member)))
			return file, nil
		})
		if err != nil {
			return queryloop.ToolCallResult{}, err
		}
		data := protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoteMain("teammate_spawned")}, protocol.JSONField{Name: "prompt", Value: input.Get("prompt")}, protocol.JSONField{Name: "teammate_id", Value: quoteMain(id)}, protocol.JSONField{Name: "agent_id", Value: quoteMain(id)}, protocol.JSONField{Name: "agent_type", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "tmux_session_name", Value: quoteMain("in-process")}, protocol.JSONField{Name: "tmux_window_name", Value: quoteMain("in-process")}, protocol.JSONField{Name: "tmux_pane_id", Value: quoteMain("in-process")}, protocol.JSONField{Name: "team_name", Value: quoteMain(team)}, protocol.JSONField{Name: "is_splitpane", Value: rawMain(false)}, protocol.JSONField{Name: "plan_mode_required", Value: rawMain(identity.PlanModeRequired)})
		return queryloop.ToolCallResult{Data: rawMain(data)}, nil
	})
	return write, err
}

func (m *MainPrint) finishNativeInProcessTask(ctx context.Context, taskID string, identity sessioncontroller.InProcessTeammateIdentity, path string, failure error, unregister runtimecleanup.Unregister) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		status, message := "completed", ""
		if failure != nil {
			status = "failed"
			message = failure.Error()
		}
		changed := false
		var toolID json.RawMessage
		if err := m.Controller.ReleaseInProcessIdleOnOwner(scope, taskID); err != nil {
			return struct{}{}, err
		}
		if err := m.Controller.UpdateBackgroundTaskOnOwner(scope, taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			toolID = task.Get("toolUseId")
			var err error
			task, changed, err = teamspawn.FinishTask(task, status, m.options.Native.NowMillis(), message)
			return task, err
		}); err != nil {
			return struct{}{}, err
		}
		if unregister != nil {
			if _, err := unregister(scope); err != nil {
				return struct{}{}, err
			}
		}
		task, err := m.Controller.BackgroundTaskOnOwner(scope, taskID)
		if err != nil {
			return struct{}{}, err
		}
		if task != nil && task.Data.String("status") != "running" && string(task.Data.Get("notified")) == "true" {
			if err = m.Controller.DeleteBackgroundTaskOnOwner(scope, taskID); err != nil {
				return struct{}{}, err
			}
		}
		if changed {
			event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(taskID)}, protocol.JSONField{Name: "tool_use_id", Value: toolID}, protocol.JSONField{Name: "status", Value: quoteMain(status)}, protocol.JSONField{Name: "output_file", Value: quoteMain("")}, protocol.JSONField{Name: "summary", Value: quoteMain(identity.AgentID)}, protocol.JSONField{Name: "owner_agent_id", Value: quoteMain(identity.AgentID)})
			if err = m.nativeBackgroundBindings().Emit(scope, event); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	_ = os.Remove(path)
	return err
}
