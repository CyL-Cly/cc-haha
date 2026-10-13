package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type TeamBindings struct {
	Enabled          func(*cooperative.Scope) (bool, error)
	ReviewRequired   bool
	IsTeammate       bool
	Start            func(context.Context, *cooperative.Scope, string, protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error)
	ConsumeTurnPause func(context.Context, *cooperative.Scope) (bool, error)
	TeamName         func(*cooperative.Scope) (string, error)
	AgentName        func(*cooperative.Scope) (string, error)
	// These storage callbacks run on the native I/O worker, never on the owner.
	TaskExecutionApproved func(string, string) (bool, error)
	TaskAssigned          func(string, protocol.JSONObject) error
	// Assignment notification errors are diagnostics after the task transaction
	// has committed. This callback runs on the owner, not the native I/O worker.
	TaskAssignmentError func(*cooperative.Scope, error)
	StageAgent          func(context.Context, *cooperative.Scope, string, protocol.JSONObject, queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error)
}

func (s *Service) teamAgent(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, call queryloop.JSONToolCallContext) (bool, *ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !s.HasTeamBindings() || s.options.Team.TeamName == nil || s.options.Team.StageAgent == nil {
		return false, nil, nil
	}
	enabled, err := s.options.Team.Enabled(scope)
	if err != nil {
		return true, nil, err
	}
	if input.String("team_name") != "" && !enabled {
		return true, nil, errors.New("Agent Teams is not yet available on your plan.")
	}
	if !enabled {
		return false, nil, nil
	}
	name := input.String("team_name")
	if name == "" {
		name, err = s.options.Team.TeamName(scope)
		if err != nil {
			return true, nil, err
		}
	}
	if name == "" || input.String("name") == "" {
		return false, nil, nil
	}
	if s.options.Team.IsTeammate {
		return true, nil, errors.New("Teammates cannot spawn other teammates — the team roster is flat. To spawn a subagent instead, omit the `name` parameter.")
	}
	future, err := s.options.Team.StageAgent(ctx, scope, name, input, call)
	return true, future, err
}

func (s *Service) BindTeam(scope *cooperative.Scope, b *TeamBindings) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if s.options.Team != nil || b == nil || b.Start == nil || b.Enabled == nil || b.ConsumeTurnPause == nil {
		return cooperative.ErrRestricted
	}
	s.options.Team = b
	return nil
}

func teamTool(name string) bool {
	return name == "TeamCreate" || name == "TeamPlan" || name == "TeamDelete"
}
func (s *Service) HasTeamBindings() bool {
	b := s.options.Team
	return b != nil && b.Enabled != nil && b.Start != nil && b.ConsumeTurnPause != nil
}

// HasTeamExecutionBindings includes the actual Agent producer, not only the
// TeamCreate/TeamPlan tool entry points. Native ordinary sessions require both.
func (s *Service) HasTeamExecutionBindings() bool {
	return s.HasTeamBindings() && s.options.Team.TeamName != nil && s.options.Team.StageAgent != nil
}
func (s *Service) startTeam(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if !s.HasTeamBindings() {
		return nil, capability("actual Team manager, plan store and turn lifecycle")
	}
	return s.options.Team.Start(ctx, scope, name, in)
}
func (s *Service) ConsumeTeamPlanTurnPause(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	if err := s.check(scope); err != nil {
		return false, err
	}
	if !s.HasTeamBindings() {
		return false, nil
	}
	return s.options.Team.ConsumeTurnPause(ctx, scope)
}
func mapTeamResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	encoded, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "text"), field("text", string(encoded)))})), nil
}
