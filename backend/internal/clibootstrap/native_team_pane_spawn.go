package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func (m *MainPrint) spawnNativePaneTeamAgent(ctx context.Context, scope *cooperative.Scope, team string, input protocol.JSONObject, call queryloop.JSONToolCallContext, backend string) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil || m.teamWorker != nil || m.options.DetachedAgent != nil {
		return nil, cooperative.ErrRestricted
	}
	if input.String("name") == "" || input.String("prompt") == "" {
		return nil, fmt.Errorf("name and prompt are required for spawn operation")
	}
	leader := m.teamLeader
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
	leaderModel, err := m.nativeTeamLeaderModel(scope)
	if err != nil {
		return nil, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return nil, err
	}
	model, err := teamspawn.ResolveModel(m.options.Native.Environment["CLAUDE_CODE_SUBAGENT_MODEL"], input.String("model"), agentModel, leaderModel, selected != nil, global.Get("teammateDefaultModel"), m.nativeTeamParsedModel, func() (string, error) { return teamspawn.HardcodedModelFallback(m.options.Native.Environment), nil })
	if err != nil {
		return nil, err
	}
	read, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
		file := leader.config.Read(team)
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
	cwd := input.String("cwd")
	if cwd == "" {
		cwd = m.workingDirectory()
	}
	binary := m.options.Native.Environment["CLAUDE_CODE_TEAMMATE_COMMAND"]
	if binary == "" {
		binary, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	mode, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return nil, err
	}
	identity := teamspawn.PaneIdentity{AgentID: id, AgentName: name, TeamName: team, Color: color, ParentSessionID: m.Controller.SessionID(), AgentType: input.String("subagent_type"), PlanModeRequired: input.String("mode") == "plan"}
	effort, err := m.initialNativeEffort()
	if err != nil {
		return nil, err
	}
	effortString := ""
	if effort != nil {
		if err = json.Unmarshal(effort, &effortString); err != nil {
			effortString = string(effort)
		}
	}
	if selected != nil && selected.Fields.Get("effort") != nil {
		value := selected.Fields.Get("effort")
		if err = json.Unmarshal(value, &effortString); err != nil {
			effortString = string(value)
		}
	}
	budget := ""
	if m.options.Arguments.Thinking.BudgetTokens != nil {
		budget = strconv.FormatFloat(*m.options.Arguments.Thinking.BudgetTokens, 'f', -1, 64)
	}
	settings := ""
	if m.options.Arguments.Sources.FlagSettingsPath != nil {
		settings = *m.options.Arguments.Sources.FlagSettingsPath
	}
	command := teamspawn.PaneSpawnCommand(teamspawn.PaneCommandOptions{Identity: identity, CWD: cwd, Binary: binary, PermissionMode: mode.String("mode"), Effort: effortString, Thinking: m.options.Arguments.Thinking.Type, ThinkingBudget: budget, Model: model, SettingsPath: settings, InlinePlugins: m.options.Arguments.PluginDirectories, Chrome: m.options.Arguments.Chrome, Environment: m.options.Native.Environment})
	paneWork, err := nativeSkillWork(m, scope, func() (string, error) {
		pane, _, err := leader.panes.CreatePane(backend, name, color)
		if err != nil {
			return "", err
		}
		return pane, leader.panes.SendCommand(backend, pane, command)
	})
	if err != nil {
		return nil, err
	}
	pane, err := paneWork.Await(scope)
	if err != nil {
		return nil, err
	}
	session, window := "claude-swarm", "swarm-view"
	if leader.panes.InsideTmux() {
		session, window = "current", "current"
	}
	snapshot := protocol.NewJSONObject()
	if m.teamState != nil {
		snapshot = m.teamState.snapshot.Clone()
	}
	roster, ok := snapshot.Object("teammates")
	if !ok {
		roster = protocol.NewJSONObject()
	} else {
		roster = roster.Clone()
	}
	snapshot.Set("teamName", quoteMain(team))
	if snapshot.Get("teamFilePath") == nil {
		snapshot.Set("teamFilePath", quoteMain(""))
	}
	if snapshot.Get("leadAgentId") == nil {
		snapshot.Set("leadAgentId", quoteMain(""))
	}
	roster.Set(id, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "agentType", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "tmuxSessionName", Value: quoteMain(session)}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain(pane)}, protocol.JSONField{Name: "cwd", Value: quoteMain(cwd)}, protocol.JSONField{Name: "spawnedAt", Value: rawMain(m.options.Native.NowMillis())})))
	snapshot.Set("teammates", rawMain(roster))
	m.teamState = &nativeTeamState{snapshot: snapshot}
	if err = m.Controller.SetLeaderTeamOnOwner(scope, team); err != nil {
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
	// Pane tasks retain Source's task type and identity, without fabricating a
	// local runAgent transcript or parent/child settings references.
	data, err := teamspawn.CreateState(teamspawn.StateOptions{ID: taskID, Name: name, Team: team, Prompt: input.String("prompt"), ParentSessionID: m.Controller.SessionID(), Color: color, OutputFile: filepath.Join(directory, taskID+".output"), Model: quoteMain(model), ToolUseID: call.ToolUseID, Now: m.options.Native.NowMillis()})
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"model", "spinnerVerb", "pastTenseVerb", "messages"} {
		data.Delete(key)
	}
	role, _ := data.Object("identity")
	role = role.Clone()
	role.Set("planModeRequired", rawMain(identity.PlanModeRequired))
	data.Set("identity", rawMain(role))
	if identity.PlanModeRequired {
		data.Set("permissionMode", quoteMain("plan"))
	}
	var once sync.Once
	done := make(chan struct{})
	cancel := func(error) {
		once.Do(func() { go func() { defer close(done); leader.panes.KillPane(backend, pane) }() })
	}
	entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return rawMain(""), nil }, Join: func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	entry.Stop = func(next *cooperative.Scope, now float64) error {
		return m.killNativeTeammateOnOwner(next, taskID, now, nil)
	}
	if err = m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
		return nil, err
	}
	if leader.mailbox == nil {
		_, _, err = m.interactiveLeaderInbox(scope)
		if err != nil {
			return nil, err
		}
	}
	member := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoteMain(id)}, protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "agentType", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "prompt", Value: input.Get("prompt")}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "planModeRequired", Value: rawMain(identity.PlanModeRequired)}, protocol.JSONField{Name: "joinedAt", Value: rawMain(m.options.Native.NowMillis())}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain(pane)}, protocol.JSONField{Name: "cwd", Value: quoteMain(cwd)}, protocol.JSONField{Name: "subscriptions", Value: rawMain([]string{})}, protocol.JSONField{Name: "backendType", Value: quoteMain(backend)})
	timestamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	return nativeSkillWork(m, scope, func() (queryloop.ToolCallResult, error) {
		_, err := leader.config.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
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
		if err = leader.mailbox.Write(name, team, protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "text", Value: input.Get("prompt")}, protocol.JSONField{Name: "timestamp", Value: quoteMain(timestamp)})); err != nil {
			return queryloop.ToolCallResult{}, err
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoteMain("teammate_spawned")}, protocol.JSONField{Name: "prompt", Value: input.Get("prompt")}, protocol.JSONField{Name: "teammate_id", Value: quoteMain(id)}, protocol.JSONField{Name: "agent_id", Value: quoteMain(id)}, protocol.JSONField{Name: "agent_type", Value: input.Get("subagent_type")}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "name", Value: quoteMain(name)}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "tmux_session_name", Value: quoteMain(session)}, protocol.JSONField{Name: "tmux_window_name", Value: quoteMain(window)}, protocol.JSONField{Name: "tmux_pane_id", Value: quoteMain(pane)}, protocol.JSONField{Name: "team_name", Value: quoteMain(team)}, protocol.JSONField{Name: "is_splitpane", Value: rawMain(true)}, protocol.JSONField{Name: "plan_mode_required", Value: rawMain(identity.PlanModeRequired)})
		return queryloop.ToolCallResult{Data: rawMain(result)}, nil
	})
}
