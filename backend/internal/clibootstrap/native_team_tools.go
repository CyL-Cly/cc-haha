package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"os"
	"path/filepath"
	"sync"
)

type nativeTeamLeaderState struct {
	config                 *teamstore.Config
	mailbox                *teamstore.Mailbox
	manager                *teamstore.Manager
	plans                  *teamplan.Store
	pauseContext           context.Context
	colors                 map[string]string
	colorIndex             int
	permissionContext      context.Context
	permissionCancel       context.CancelFunc
	permissionTools        map[string]bool
	panes                  *teamspawn.PaneRuntime
	shutdownPromptInjected bool
	mode                   string
	preferTmux             bool
}

func (m *MainPrint) nativeTeamBindings(scope *cooperative.Scope) (*builtintools.TeamBindings, error) {
	if !scope.BelongsTo(m.Owner) || m.taskStore == nil {
		return nil, cooperative.ErrRestricted
	}
	if m.teamLeader == nil {
		n := m.options.Native
		directory := n.Environment["CLAUDE_CONFIG_DIR"]
		if directory == "" {
			directory = filepath.Join(n.Home, ".claude")
		}
		config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: directory, TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return nil, err
		}
		plans, err := teamplan.New(teamplan.Options{Config: config, Tasks: m.taskStore, ConfigDir: directory, WorkDir: m.workingDirectory(), Now: n.Now, UUID: func() (string, error) { return uuidMain(), nil }})
		if err != nil {
			return nil, err
		}
		worktrees, err := m.nativeWorktreeService()
		if err != nil {
			return nil, err
		}
		panes, err := teamspawn.NewPaneRuntime(m.workingDirectory(), m.options.PID, n.Environment)
		if err != nil {
			return nil, err
		}
		lifecycle, err := teamstore.NewLifecycle(teamstore.LifecycleOptions{Config: config, Tasks: m.taskStore, RuntimeResources: func(ctx context.Context, _ string, members []protocol.JSONObject, kill bool) error {

			var paneJobs sync.WaitGroup
			if kill {
				for _, member := range members {
					if member.String("name") == "team-lead" || member.String("tmuxPaneId") == "" || (member.String("backendType") != "tmux" && member.String("backendType") != "iterm2") {
						continue
					}
					member := member.Clone()
					paneJobs.Add(1)
					go func() {
						defer paneJobs.Done()
						panes.KillPane(member.String("backendType"), member.String("tmuxPaneId"))
					}()
				}
			}
			paneJobs.Wait()
			for _, member := range members {
				path := member.String("worktreePath")
				if path == "" {
					continue
				}
				if worktrees == nil || !filepath.IsAbs(path) {
					return mainCap("Team cleanup native worktree path")
				}
				if err := worktrees.DestroyTeamWorktree(ctx, path, func(path string) error {
					if err := n.check("Team worktree cleanup", path); err != nil {
						return err
					}
					return os.RemoveAll(path)
				}); err != nil {
					return err
				}
			}
			return nil
		}})
		if err != nil {
			return nil, err
		}
		manager, err := teamstore.NewManager(teamstore.ManagerOptions{Config: config, Tasks: m.taskStore, Lifecycle: lifecycle, Now: n.Now, WordSlug: sessionplans.GenerateWordSlug})
		if err != nil {
			return nil, err
		}
		global, err := m.Global.Get()
		if err != nil {
			return nil, err
		}
		mode := m.options.Arguments.TeammateMode
		if mode == "" {
			mode = global.String("teammateMode")
		}
		if mode == "" {
			mode = "auto"
		}
		m.teamLeader = &nativeTeamLeaderState{mode: mode, preferTmux: string(global.Get("preferTmuxOverIterm2")) == "true", config: config, plans: plans, manager: manager, panes: panes, colors: map[string]string{}}
		if _, err = m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
			return nativeSkillWork(m, next, func() (struct{}, error) { return struct{}{}, manager.CleanupSession(context.Background()) })
		})); err != nil {
			return nil, err
		}
	}
	return &builtintools.TeamBindings{TeamName: func(next *cooperative.Scope) (string, error) {
		if !next.BelongsTo(m.Owner) {
			return "", cooperative.ErrRestricted
		}
		return m.nativeLeaderTeamName(), nil
	}, StageAgent: m.stageNativeTeamAgent, Enabled: m.nativeSendMessageEnabled, ReviewRequired: m.options.Native.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] == "1", IsTeammate: m.teamWorker != nil || m.nativeInProcessTeammateIdentity() != nil, Start: m.startNativeTeamTool, ConsumeTurnPause: func(ctx context.Context, next *cooperative.Scope) (bool, error) {
		if !next.BelongsTo(m.Owner) {
			return false, cooperative.ErrRestricted
		}
		pause := m.teamLeader.pauseContext == ctx
		m.teamLeader.pauseContext = nil
		return pause, nil
	}}, nil
}
func (m *MainPrint) nativeLeaderTeamName() string {
	if teammate := m.nativeInProcessTeammateIdentity(); teammate != nil {
		return teammate.TeamName
	}
	if m.teamWorker != nil {
		return m.teamWorker.preset.Start.TeamName
	}
	if m.teamState != nil {
		return m.teamState.snapshot.String("teamName")
	}
	return ""
}
func (m *MainPrint) nativeTeamCatalog(scope *cooperative.Scope) (protocol.JSONObject, error) {
	definitions, err := m.agents.List(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return teamworker.SnapshotAgents(definitions.Active, func(agent agentcatalog.Definition) (string, error) { return m.nativeAgentPrompt(scope, agent) })
}
func (m *MainPrint) nativeTeamProposal(scope *cooperative.Scope, proposal *protocol.JSONObject, model string) (protocol.JSONObject, protocol.JSONObject, error) {
	// TeamCreate without a proposal snapshots presets before validating the
	// desktop runtime. resolveProposedTeamPlan performs the inverse order.
	if proposal == nil {
		catalog, err := m.nativeTeamCatalog(scope)
		if err != nil {
			return protocol.JSONObject{}, catalog, err
		}
		leader, err := teamworker.LeaderRuntime(m.options.Native.Environment, model)
		return leader, protocol.NewJSONObject(protocol.JSONField{Name: "agentCatalog", Value: rawMain(catalog)}), err
	}
	leader, err := teamworker.LeaderRuntime(m.options.Native.Environment, model)
	if err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, err
	}
	catalog, err := m.nativeTeamCatalog(scope)
	if err != nil {
		return leader, catalog, err
	}
	patch, err := teamworker.ResolveProposal(*proposal, catalog, leader, m.options.Native.Environment, m.nativeTeamParsedModel)
	return leader, patch, err
}

type nativeTeamToolOutput struct {
	data    protocol.JSONObject
	created *teamstore.Created
	clear   bool
}

func (m *MainPrint) startNativeTeamTool(ctx context.Context, scope *cooperative.Scope, name string, input protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if name == "TeamPlan" {
		return m.startNativeTeamPlan(ctx, scope, input)
	}
	model, err := m.Host.InferenceModel(scope)
	if err != nil {
		return nil, err
	}
	model, err = m.nativeTeamParsedModel(model)
	if err != nil {
		return nil, err
	}
	existing := m.nativeLeaderTeamName()
	session := m.Controller.SessionID()
	cwd := m.workingDirectory()
	leader := m.teamLeader
	var proposal *protocol.JSONObject
	if value := input.Get("plan"); value != nil {
		var parsed protocol.JSONObject
		if err := parsed.UnmarshalJSON(value); err != nil {
			return nil, err
		}
		proposal = &parsed
	}
	review := m.options.Native.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] == "1"
	var runtime, patch protocol.JSONObject
	if name == "TeamCreate" {
		if existing != "" {
			return nil, fmt.Errorf("Already leading team %q. A leader can only manage one team at a time. Use TeamDelete to end the current team before creating a new one.", existing)
		}
		if proposal != nil && !review {
			return nil, errors.New("Team plan review requires a desktop session.")
		}
		if review {
			runtime, patch, err = m.nativeTeamProposal(scope, proposal, model)
			if err != nil {
				return nil, err
			}
			if proposal != nil {
				if err = teamplan.ValidateGraph(patch, false); err != nil {
					return nil, err
				}
			}
		}
	}
	request := teamstore.CreationRequest{TeamName: input.String("team_name"), Description: input.String("description"), HasDescription: input.Get("description") != nil, AgentType: input.String("agent_type"), LeaderModel: model, CWD: cwd, SessionID: session, ExistingTeam: existing}
	work, err := nativeSkillWork(m, scope, func() (nativeTeamToolOutput, error) {
		if err := context.Cause(ctx); err != nil {
			return nativeTeamToolOutput{}, err
		}
		switch name {
		case "TeamCreate":
			created, err := leader.manager.Create(ctx, request)
			if err != nil {
				return nativeTeamToolOutput{}, err
			}
			data := protocol.NewJSONObject(protocol.JSONField{Name: "team_name", Value: quoteMain(created.TeamName)}, protocol.JSONField{Name: "team_file_path", Value: quoteMain(leader.config.Path(created.TeamName))}, protocol.JSONField{Name: "lead_agent_id", Value: quoteMain(created.LeadAgentID)})
			if review {
				initial := patch.Clone()
				initial.Set("workDir", quoteMain(cwd))
				draft, err := leader.plans.EnsureDraft(created.TeamName, session, runtime, initial)
				if err != nil {
					return nativeTeamToolOutput{}, err
				}
				data.Set("plan", rawMain(teamworker.PlanToolResult(draft)))
			}
			return nativeTeamToolOutput{data: data, created: &created}, nil
		case "TeamDelete":
			data, err := leader.manager.Delete(ctx, existing)
			return nativeTeamToolOutput{data: data, clear: string(data.Get("success")) == "true"}, err
		default:
			return nativeTeamToolOutput{}, mainCap("unknown native Team tool")
		}
	})
	if err != nil {
		return nil, err
	}
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, err
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, result nativeTeamToolOutput, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		if result.created != nil {
			created := result.created
			if err := m.Controller.SetLeaderTeamOnOwner(next, teamstore.SanitizeName(created.TeamName)); err != nil {
				return out.Reject(next, err)
			}
			colors := []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}
			color := leader.colors[created.LeadAgentID]
			if color == "" {
				color = colors[leader.colorIndex%len(colors)]
				leader.colorIndex++
				leader.colors[created.LeadAgentID] = color
			}
			kind := request.AgentType
			if kind == "" {
				kind = "team-lead"
			}
			teammate := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "agentType", Value: quoteMain(kind)}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "tmuxSessionName", Value: quoteMain("")}, protocol.JSONField{Name: "tmuxPaneId", Value: quoteMain("")}, protocol.JSONField{Name: "cwd", Value: quoteMain(cwd)}, protocol.JSONField{Name: "spawnedAt", Value: rawMain(m.options.Native.NowMillis())})
			m.teamState = &nativeTeamState{snapshot: protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: quoteMain(created.TeamName)}, protocol.JSONField{Name: "teamFilePath", Value: quoteMain(leader.config.Path(created.TeamName))}, protocol.JSONField{Name: "leadAgentId", Value: quoteMain(created.LeadAgentID)}, protocol.JSONField{Name: "teammates", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: created.LeadAgentID, Value: rawMain(teammate)}))})}
		}
		if result.created != nil && m.options.Interactive && m.options.OnLeaderInboxReady != nil {
			if err := m.options.OnLeaderInboxReady(); err != nil {
				return out.Reject(next, err)
			}
		}

		if result.clear {
			if err := m.Controller.SetLeaderTeamOnOwner(next, ""); err != nil {
				return out.Reject(next, err)
			}
			m.teamState = nil
			clear(leader.colors)
			leader.colorIndex = 0
		}
		return out.Resolve(next, queryloop.ToolCallResult{Data: rawMain(result.data)})
	})
	return out, err
}
func (m *MainPrint) nativeTeamParsedModel(model string) (string, error) {
	parsed, err := m.nativeModelSelector.Parse(quoteMain(model))
	if err != nil {
		return "", err
	}
	return decodeNativeModel(parsed)
}

func (m *MainPrint) startNativeTeamPlan(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if m.teamWorker != nil {
		return nil, errors.New("Only the team leader can submit a team plan.")
	}
	team, session, operation := input.String("team_name"), m.Controller.SessionID(), input.String("operation")
	leader := m.teamLeader
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, err
	}
	read, err := nativeSkillWork(m, scope, func() (*protocol.JSONObject, error) { return leader.plans.Read(team) })
	if err != nil {
		return nil, err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, plan *protocol.JSONObject, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		if plan == nil || plan.String("sessionId") != session {
			return out.Reject(next, errors.New("No team draft belongs to this session. Create the team first."))
		}
		if operation == "get" {
			return out.Resolve(next, queryloop.ToolCallResult{Data: rawMain(teamworker.PlanToolResult(*plan))})
		}
		if input.Get("expected_revision") == nil {
			return out.Reject(next, errors.New("expected_revision is required. Read the latest plan before editing it."))
		}
		var proposal *protocol.JSONObject
		if value := input.Get("plan"); value != nil {
			var parsed protocol.JSONObject
			if err := parsed.UnmarshalJSON(value); err != nil {
				return out.Reject(next, err)
			}
			proposal = &parsed
		}
		if proposal == nil && operation == "replace" {
			return out.Reject(next, errors.New("A complete plan is required for replace."))
		}
		identity := teamplan.CurrentIdentity(*plan)
		_ = json.Unmarshal(input.Get("expected_revision"), &identity.ExpectedRevision)
		commit := func(active *cooperative.Scope, tasks []protocol.JSONObject) error {
			var patch protocol.JSONObject
			if proposal != nil {
				model, err := m.Host.InferenceModel(active)
				if err != nil {
					return out.Reject(active, err)
				}
				_, patch, err = m.nativeTeamProposal(active, proposal, model)
				if err != nil {
					return out.Reject(active, err)
				}
			} else {
				catalog, err := m.nativeTeamCatalog(active)
				if err != nil {
					return out.Reject(active, err)
				}
				patch = protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: rawMain(tasks)}, protocol.JSONField{Name: "agentCatalog", Value: rawMain(catalog)})
			}
			edit, err := nativeSkillWork(m, active, func() (protocol.JSONObject, error) {
				if operation == "submit" {
					return leader.plans.Submit(team, identity, &patch)
				}
				return leader.plans.Replace(team, identity, patch, false)
			})
			if err != nil {
				return out.Reject(active, err)
			}
			_, err = edit.Then(active, func(done *cooperative.Scope, record protocol.JSONObject, failure error) error {
				if failure != nil {
					return out.Reject(done, failure)
				}
				if operation == "submit" {
					leader.pauseContext = ctx
				}
				return out.Resolve(done, queryloop.ToolCallResult{Data: rawMain(teamworker.PlanToolResult(record))})
			})
			return err
		}
		if proposal != nil {
			return commit(next, nil)
		}
		// The original await listTasks/captureLegacyPlanTasks precedes the
		// catalogue snapshot. Preserve both physical I/O and error ordering.
		capture, err := nativeSkillWork(m, next, func() ([]protocol.JSONObject, error) {
			return teamworker.CaptureLegacyTasks(*plan, m.taskStore)
		})
		if err != nil {
			return out.Reject(next, err)
		}
		_, err = capture.Then(next, func(active *cooperative.Scope, tasks []protocol.JSONObject, failure error) error {
			if failure != nil {
				return out.Reject(active, failure)
			}
			return commit(active, tasks)
		})
		return err
	})
	return out, err
}
