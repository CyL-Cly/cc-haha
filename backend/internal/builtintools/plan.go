package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
)

type PlanModeBindings struct {
	Plans           *sessionplans.Service
	AgentID         string
	CheckInactive   func(*cooperative.Scope) error
	Enter, Exit     func(*cooperative.Scope) error
	InterviewPhase  func() (bool, error)
	HasTaskTool     func(*cooperative.Scope) (bool, error)
	Teammate        func(*cooperative.Scope) (bool, bool, error)
	RequestApproval func(*cooperative.Scope, json.RawMessage, string) (json.RawMessage, error)
}

func (s *Service) HasPlanBindings() bool {
	return s != nil && s.options.PlanMode != nil && s.options.PlanMode.Plans != nil && s.options.PlanMode.CheckInactive != nil && s.options.PlanMode.Enter != nil && s.options.PlanMode.Exit != nil && s.options.PlanMode.InterviewPhase != nil && s.options.PlanMode.HasTaskTool != nil
}
func planTool(name string) bool { return name == "EnterPlanMode" || name == "ExitPlanMode" }
func (s *Service) validatePlanMode(scope *cooperative.Scope, name string) error {
	if !s.HasPlanBindings() {
		return capability("actual shared PlanMode host")
	}
	if name == "ExitPlanMode" {
		permission, e := s.options.PermissionContext(scope)
		if e != nil {
			return e
		}
		if permission.String("mode") != "plan" {
			return errors.New("You are not in plan mode. This tool is only for exiting plan mode after writing a plan. If your plan was already approved, continue with implementation.")
		}
	}
	return nil
}
func (s *Service) callPlanMode(ctx context.Context, name string, in protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	future, e := s.startPlanMode(ctx, name, in, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
	if e != nil {
		return nil, e
	}
	result, e := future.Await(scope)
	return result.Data, e
}

func (s *Service) startPlanMode(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !s.HasPlanBindings() {
		return nil, capability("actual shared PlanMode host")
	}
	p := s.options.PlanMode
	if name == "EnterPlanMode" {
		if p.AgentID != "" {
			return nil, errors.New("EnterPlanMode tool cannot be used in agent contexts")
		}
		if e := p.CheckInactive(scope); e != nil {
			return nil, e
		}
		if e := p.Enter(scope); e != nil {
			return nil, e
		}
		future, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
		if e != nil {
			return nil, e
		}
		e = future.Resolve(scope, queryloop.ToolCallResult{Data: object(field("message", "Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach."))})
		return future, e
	}
	path, e := p.Plans.Path(scope, p.AgentID)
	if e != nil {
		return nil, e
	}
	var plan json.RawMessage
	edited := false
	if candidate := in.Get("plan"); candidate != nil {
		if _, e := protocol.DecodeJavaScriptJSONString(candidate); e == nil {
			plan = candidate
			edited = true
		}
	}
	if !edited {
		plan, _, e = p.Plans.Read(scope, p.AgentID)
		if e != nil {
			return nil, e
		}
	}
	finish := func(current *cooperative.Scope) (queryloop.ToolCallResult, error) {
		data, e := s.finishPlanExit(current, plan, path, edited)
		return queryloop.ToolCallResult{Data: data}, e
	}
	if edited {
		write, e := p.Plans.PrepareEditedWrite(scope, p.AgentID, plan)
		if e != nil {
			return nil, e
		}
		pending, e := native(s, scope, func() (error, error) { return write(), nil })
		if e != nil {
			return nil, e
		}
		return transform(s, scope, pending, func(current *cooperative.Scope, failure error) (queryloop.ToolCallResult, error) {
			if e := p.Plans.LogEditedWriteError(current, failure); e != nil {
				return queryloop.ToolCallResult{}, e
			}
			return finish(current)
		})
	}
	future, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if e != nil {
		return nil, e
	}
	result, e := finish(scope)
	if e != nil {
		e = future.Reject(scope, e)
	} else {
		e = future.Resolve(scope, result)
	}
	return future, e
}

func (s *Service) finishPlanExit(scope *cooperative.Scope, plan json.RawMessage, path string, edited bool) (json.RawMessage, error) {
	p := s.options.PlanMode
	if p.Teammate != nil {
		teammate, required, e := p.Teammate(scope)
		if e != nil {
			return nil, e
		}
		if teammate && required {
			if p.RequestApproval == nil {
				return nil, capability("actual teammate plan approval mailbox producer")
			}
			return p.RequestApproval(scope, plan, path)
		}
	}
	if e := p.CheckInactive(scope); e != nil {
		return nil, e
	}
	if e := p.Exit(scope); e != nil {
		return nil, e
	}
	hasTask, e := p.HasTaskTool(scope)
	if e != nil {
		return nil, e
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "plan", Value: plan}, field("isAgent", p.AgentID != ""), field("filePath", path))
	if hasTask {
		result.Set("hasTaskTool", json.RawMessage("true"))
	}
	if edited {
		result.Set("planWasEdited", json.RawMessage("true"))
	}
	return raw(result), nil
}
func (s *Service) mapPlanResult(scope *cooperative.Scope, name string, data, id json.RawMessage) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	if e := result.UnmarshalJSON(data); e != nil {
		return protocol.JSONObject{}, e
	}
	if name == "EnterPlanMode" {
		interview, e := s.options.PlanMode.InterviewPhase()
		if e != nil {
			return protocol.JSONObject{}, e
		}
		instructions := result.String("message")
		if interview {
			instructions += "\n\nDO NOT write or edit any files except the plan file. Detailed workflow instructions will follow."
		} else {
			instructions += "\n\nIn plan mode, you should:\n1. Thoroughly explore the codebase to understand existing patterns\n2. Identify similar features and architectural approaches\n3. Consider multiple approaches and their trade-offs\n4. Use AskUserQuestion if you need to clarify the approach\n5. Design a concrete implementation strategy\n6. When ready, use ExitPlanMode to present your plan for approval\n\nRemember: DO NOT write or edit any files yet. This is a read-only exploration and planning phase."
		}
		return protocol.NewJSONObject(field("type", "tool_result"), field("content", instructions), protocol.JSONField{Name: "tool_use_id", Value: id}), nil
	}
	content := json.RawMessage(nil)
	if string(result.Get("awaitingLeaderApproval")) == "true" {
		content = quote("Your plan has been submitted to the team lead for approval.\n\nPlan file: " + result.String("filePath") + "\n\n**What happens next:**\n1. Wait for the team lead to review your plan\n2. You will receive a message in your inbox with approval/rejection\n3. If approved, you can proceed with implementation\n4. If rejected, refine your plan based on the feedback\n\n**Important:** Do NOT proceed until you receive approval. Check your inbox for response.\n\nRequest ID: " + result.String("requestId"))
	} else if string(result.Get("isAgent")) == "true" {
		content = quote(`User has approved the plan. There is nothing else needed from you now. Please respond with "ok"`)
	} else {
		plan := result.Get("plan")
		units, e := protocol.DecodeJavaScriptJSONString(plan)
		if string(plan) == "null" || e == nil && planWhitespace(units) {
			content = quote("User has approved exiting plan mode. You can now proceed.")
		} else {
			if e != nil {
				return protocol.JSONObject{}, e
			}
			label := "Approved Plan"
			if string(result.Get("planWasEdited")) == "true" {
				label = "Approved Plan (edited by user)"
			}
			hint := ""
			if string(result.Get("hasTaskTool")) == "true" {
				hint = "\n\nIf this plan can be broken down into multiple independent tasks, consider using the TeamCreate tool to create a team and parallelize the work."
			}
			prefix := quote("User has approved your plan. You can now start coding. Start with updating your todo list if applicable\n\nYour plan has been saved to: " + result.String("filePath") + "\nYou can refer back to it if needed during implementation." + hint + "\n\n## " + label + ":\n")
			content, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{prefix, plan}, "")
			if e != nil {
				return protocol.JSONObject{}, e
			}
		}
	}
	return protocol.NewJSONObject(field("type", "tool_result"), protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "tool_use_id", Value: id}), nil
}

func planWhitespace(units []uint16) bool {
	for _, unit := range units {
		if !strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", rune(unit)) {
			return false
		}
	}
	return true
}
