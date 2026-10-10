package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"strings"
)

func (m *MainPrint) stageNativeTeamAgent(ctx context.Context, scope *cooperative.Scope, team string, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil {
		return nil, cooperative.ErrRestricted
	}
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, err
	}
	leader := m.teamLeader
	session, cwd := m.Controller.SessionID(), m.workingDirectory()
	request := input.Clone()
	read, err := nativeSkillWork(m, scope, func() (*protocol.JSONObject, error) {
		config := leader.config.Read(team)
		if config == nil {
			return nil, fmt.Errorf("Team %q does not exist. Call TeamCreate first, or omit team_name to launch an ordinary subagent.", team)
		}
		return leader.plans.Read(team)
	})
	if err != nil {
		return nil, err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, existing *protocol.JSONObject, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		if m.options.Native.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] != "1" && existing == nil {
			global, err := m.Global.Get()
			if err != nil {
				return out.Reject(next, err)
			}
			leader.preferTmux = string(global.Get("preferTmuxOverIterm2")) == "true"
			selectBackend, err := nativeSkillWork(m, next, func() (string, error) {
				return leader.panes.SelectBackend(leader.mode, !m.options.Interactive, leader.preferTmux)
			})
			if err != nil {
				return out.Reject(next, err)
			}
			backend, err := selectBackend.Await(next)
			if err != nil {
				return out.Reject(next, err)
			}
			backend, err = m.configureNativeTeamPane(ctx, next, backend)
			if err != nil {
				return out.Reject(next, err)
			}
			if backend != "in-process" {
				spawn, err := m.spawnNativePaneTeamAgent(ctx, next, team, request, call, backend)
				if err != nil {
					return out.Reject(next, err)
				}
				_, err = spawn.Then(next, func(done *cooperative.Scope, result queryloop.ToolCallResult, failure error) error {
					if failure != nil {
						return out.Reject(done, failure)
					}
					return out.Resolve(done, result)
				})
				return err
			}
			spawn, err := m.spawnNativeInProcessTeamAgent(ctx, next, team, request, call)
			if err != nil {
				return out.Reject(next, err)
			}
			_, err = spawn.Then(next, func(done *cooperative.Scope, result queryloop.ToolCallResult, failure error) error {
				if failure != nil {
					return out.Reject(done, failure)
				}
				return out.Resolve(done, result)
			})
			return err
		}
		if existing != nil && existing.String("sessionId") != session {
			return out.Reject(next, errors.New("Only the owning team leader can propose members."))
		}
		catalog, err := m.nativeTeamCatalog(next)
		if err != nil {
			return out.Reject(next, err)
		}
		typ := request.String("subagent_type")
		if typ == "" {
			typ = "general-purpose"
		}
		var snapshot protocol.JSONObject
		_ = snapshot.UnmarshalJSON(catalog.Get(typ))
		if !snapshot.IsObject() {
			return out.Reject(next, fmt.Errorf("Agent preset '%s' is unavailable.", typ))
		}
		if message := snapshot.String("configurationError"); message != "" {
			return out.Reject(next, errors.New(message))
		}
		var runtime protocol.JSONObject
		if existing != nil {
			_ = runtime.UnmarshalJSON(existing.Get("leaderRuntime"))
		} else {
			runtime, err = teamworker.LeaderRuntime(m.options.Native.Environment, "")
			if err != nil {
				return out.Reject(next, err)
			}
		}
		model := runtime.String("modelId")
		envModel := protocol.TrimJavaScriptString(m.options.Native.Environment["CLAUDE_CODE_SUBAGENT_MODEL"])
		inputModel := protocol.TrimJavaScriptString(request.String("model"))
		selected := protocol.TrimJavaScriptString(snapshot.String("model"))
		var candidate string
		if envModel != "" && !strings.EqualFold(envModel, "inherit") {
			candidate = envModel
		} else if inputModel != "" {
			if !strings.EqualFold(inputModel, "inherit") {
				candidate = inputModel
			}
		} else if selected != "" && !strings.EqualFold(selected, "inherit") {
			candidate = selected
		}
		if candidate != "" {
			model, err = m.nativeTeamParsedModel(candidate)
			if err != nil {
				return out.Reject(next, err)
			}
		}
		memberRuntime := runtime.Clone()
		memberRuntime.Set("modelId", quoteMain(model))
		if effort := snapshot.Get("effortLevel"); effort != nil {
			memberRuntime.Set("effortLevel", effort)
		} else if model != runtime.String("modelId") {
			memberRuntime.Delete("effortLevel")
		}
		member := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: request.Get("name")}, protocol.JSONField{Name: "name", Value: request.Get("name")}, protocol.JSONField{Name: "agentType", Value: quoteMain(typ)}, protocol.JSONField{Name: "prompt", Value: request.Get("prompt")}, protocol.JSONField{Name: "runtime", Value: rawMain(memberRuntime)}, protocol.JSONField{Name: "suggestedRuntime", Value: rawMain(memberRuntime.Clone())}, protocol.JSONField{Name: "agentSnapshot", Value: rawMain(snapshot)})
		write, err := nativeSkillWork(m, next, func() (protocol.JSONObject, error) {
			if _, err := leader.plans.EnsureDraft(team, session, runtime, protocol.NewJSONObject(protocol.JSONField{Name: "workDir", Value: quoteMain(cwd)}, protocol.JSONField{Name: "agentCatalog", Value: rawMain(catalog)})); err != nil {
				return protocol.JSONObject{}, err
			}
			return leader.plans.StageMember(team, member)
		})
		if err != nil {
			return out.Reject(next, err)
		}
		_, err = write.Then(next, func(done *cooperative.Scope, draft protocol.JSONObject, failure error) error {
			if failure != nil {
				return out.Reject(done, failure)
			}
			data := protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoteMain("teammate_staged")}, protocol.JSONField{Name: "prompt", Value: request.Get("prompt")}, protocol.JSONField{Name: "staged", Value: rawMain(true)}, protocol.JSONField{Name: "plan_id", Value: draft.Get("planId")}, protocol.JSONField{Name: "revision", Value: draft.Get("revision")}, protocol.JSONField{Name: "message", Value: quoteMain("Member recorded in the team draft, NOT started. Complete all members and tasks, then submit the whole plan with TeamPlan. Only human confirmation starts execution.")}, protocol.JSONField{Name: "teammate_id", Value: quoteMain("")}, protocol.JSONField{Name: "agent_id", Value: quoteMain("")}, protocol.JSONField{Name: "agent_type", Value: quoteMain(typ)}, protocol.JSONField{Name: "name", Value: request.Get("name")}, protocol.JSONField{Name: "team_name", Value: quoteMain(team)}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "tmux_session_name", Value: quoteMain("")}, protocol.JSONField{Name: "tmux_window_name", Value: quoteMain("")}, protocol.JSONField{Name: "tmux_pane_id", Value: quoteMain("")})
			return out.Resolve(done, queryloop.ToolCallResult{Data: rawMain(data)})
		})
		return err
	})
	return out, err
}
