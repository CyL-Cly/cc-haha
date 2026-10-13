package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

type nativeGoalRuntime struct {
	state         *goalstate.State
	trustAccepted bool
}

func goalHookObject(hook *goalstate.Hook) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("prompt")}, protocol.JSONField{Name: "prompt", Value: quoteMain(hook.Prompt)}, protocol.JSONField{Name: "timeout", Value: rawMain(hook.Timeout)})
}
func (m *MainPrint) nativeGoalState(scope *cooperative.Scope) (*goalstate.State, error) {
	if !scope.BelongsTo(m.Owner) || m.hook == nil || !m.hook.HasNativePromptBindings(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.nativeGoals == nil {
		state, err := goalstate.New(goalstate.Options{Owner: m.Owner, Add: func(scope *cooperative.Scope, id string, hook *goalstate.Hook, done func(*cooperative.Scope) error) error {
			return m.hook.RegisterNativePromptHook(scope, id, "Stop", "", goalHookObject(hook), done)
		}, Remove: func(scope *cooperative.Scope, id string, hook *goalstate.Hook) error {
			return m.hook.RemoveNativePromptHook(scope, id, "Stop", goalHookObject(hook))
		}})
		if err != nil {
			return nil, err
		}
		m.nativeGoals = &nativeGoalRuntime{state: state}
	}
	return m.nativeGoals.state, nil
}
func (m *MainPrint) nativePromptHookBindings() *queryhooks.PromptBindings {
	if m.options.Native == nil || !m.options.Native.NativeProcess {
		return nil
	}
	return &queryhooks.PromptBindings{Owner: m.Owner, TeamWorkInProgress: func(ctx context.Context, call queryhooks.Context) (bool, error) {
		return m.nativeTeamWorkInProgress(call.Scope)
	}, Query: func(ctx context.Context, scope *cooperative.Scope, input queryhooks.PromptModelInput) (protocol.JSONObject, error) {
		if !scope.BelongsTo(m.Owner) || m.Host == nil {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		model := ""
		if value := input.Hook.Get("model"); value != nil && string(value) != "null" {
			model = input.Hook.String("model")
		} else {
			settings, err := m.Startup.GetInitialSettings()
			if err != nil {
				return protocol.JSONObject{}, err
			}
			environment := map[string]string{}
			snapshot := m.environment.Snapshot()
			for _, field := range snapshot.OwnProperties() {
				var key, value string
				_ = json.Unmarshal(field.NameJSON, &key)
				_ = json.Unmarshal(field.Value, &value)
				environment[key] = value
			}
			selector, err := modelselection.NewParsing(modelselection.Options{Environment: environment, Settings: settings.Clone()})
			if err != nil {
				return protocol.JSONObject{}, err
			}
			model = selector.SmallFastModel()
		}
		return m.Host.QueryPromptHook(ctx, scope, input, model)
	}, RestoreGoal: func(ctx context.Context, call queryhooks.Context) (bool, error) {
		if !call.Scope.BelongsTo(m.Owner) {
			return true, cooperative.ErrRestricted
		}
		state, err := m.nativeGoalState(call.Scope)
		if err != nil {
			return true, err
		}
		records := []protocol.JSONObject{}
		for _, message := range call.History {
			data, err := message.MarshalJSON()
			if err != nil {
				return true, err
			}
			var record protocol.JSONObject
			if err = record.UnmarshalJSON(data); err != nil {
				return true, err
			}
			records = append(records, record)
		}
		for _, assistant := range call.Assistants {
			data, err := assistant.MarshalJSON()
			if err != nil {
				return true, err
			}
			var record protocol.JSONObject
			if err = record.UnmarshalJSON(data); err != nil {
				return true, err
			}
			records = append(records, record)
		}
		goal, err := state.Restore(call.Scope, m.Controller.SessionID(), records, m.options.Native.NowMillis())
		return goal == nil, err
	}}
}
