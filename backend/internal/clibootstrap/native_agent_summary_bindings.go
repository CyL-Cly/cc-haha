package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) nativeSDKAgentSummaryFeatureGate(scope *cooperative.Scope) bool {
	if !scope.BelongsTo(m.Owner) {
		return false
	}
	env := m.nativeOpenAIEnvironment()
	// The external distribution has no GrowthBook client key. Its cached gate
	// default is true; only the existing internal override producer can replace it.
	if env["USER_TYPE"] != "ant" {
		return true
	}
	const name = "tengu_slate_prism"
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON([]byte(env["CLAUDE_INTERNAL_FC_OVERRIDES"])) == nil && overrides.Get(name) != nil {
		return openaiauth.AuthTruthy(overrides.Get(name))
	}
	if global, err := m.Global.Get(); err == nil && global != nil {
		if overrides.UnmarshalJSON(global.Get("growthBookOverrides")) == nil && overrides.Get(name) != nil {
			return openaiauth.AuthTruthy(overrides.Get(name))
		}
	}
	return true
}

func (m *MainPrint) nativeAgentSummaryBindings() *sdkhost.NativeAgentSummaryBindings {
	d := m.options.DetachedAgent
	if d == nil || !d.agentSummary || m.options.AgentID == "" {
		return nil
	}
	id := m.options.AgentID
	return &sdkhost.NativeAgentSummaryBindings{
		Load: func(ctx context.Context, scope *cooperative.Scope) ([]protocol.JSONObject, error) {
			if !scope.BelongsTo(m.Owner) {
				return nil, cooperative.ErrRestricted
			}
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			path, loader, _, err := m.Controller.AgentTranscriptSourceOnOwner(scope, id)
			if err != nil {
				return nil, err
			}
			future, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
				rows := []protocol.JSONObject{}
				if transcript := loader.LoadAgentTranscript(path, id); transcript != nil {
					for _, data := range transcript.Messages {
						var row protocol.JSONObject
						if err := row.UnmarshalJSON(data); err != nil {
							return nil, err
						}
						rows = append(rows, row)
					}
				}
				return rows, nil
			})
			if err != nil {
				return nil, err
			}
			return future.Await(scope)
		},
		Update: func(scope *cooperative.Scope, summary json.RawMessage) error {
			return m.updateNativeAgentSummary(scope, id, summary)
		},
	}
}

// One parent-Owner call contains both the running-state mutation and SDK emit.
// No task can become terminal between those two Source-synchronous operations.
func (m *MainPrint) updateNativeAgentSummary(scope *cooperative.Scope, id string, summary json.RawMessage) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if d := m.options.DetachedAgent; d != nil {
		return bridgeAgentVoid(m, d.authParent, scope, func(next *cooperative.Scope) error { return d.authParent.updateNativeAgentSummary(next, id, summary) })
	}
	var event protocol.JSONObject
	err := m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("status") != "running" {
			return task, nil
		}
		task = task.Clone()
		progress, _ := task.Object("progress")
		if !progress.IsObject() {
			progress = protocol.NewJSONObject()
		} else {
			progress = progress.Clone()
		}
		for _, key := range []string{"tokenCount", "toolUseCount"} {
			if progress.Get(key) == nil || string(progress.Get(key)) == "null" {
				progress.Set(key, rawMain(0))
			}
		}
		progress.Set("summary", append(json.RawMessage(nil), summary...))
		task.Set("progress", rawMain(progress))
		var tokens, tools, start float64
		_ = json.Unmarshal(progress.Get("tokenCount"), &tokens)
		_ = json.Unmarshal(progress.Get("toolUseCount"), &tools)
		_ = json.Unmarshal(task.Get("startTime"), &start)
		event = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_progress")}, protocol.JSONField{Name: "task_id", Value: quoteMain(id)}, protocol.JSONField{Name: "description", Value: append(json.RawMessage(nil), summary...)}, protocol.JSONField{Name: "summary", Value: append(json.RawMessage(nil), summary...)}, protocol.JSONField{Name: "usage", Value: rawMain(map[string]any{"total_tokens": tokens, "tool_uses": tools, "duration_ms": m.options.Native.NowMillis() - start})})
		if value := task.Get("toolUseId"); value != nil {
			event.Set("tool_use_id", append(json.RawMessage(nil), value...))
		}
		if value := task.Get("ownerAgentId"); openaiauth.AuthTruthy(value) {
			event.Set("owner_agent_id", append(json.RawMessage(nil), value...))
		}
		return task, nil
	})
	if err != nil || !event.IsObject() || !m.nativeSDKAgentSummaryEnabled.Load() {
		return err
	}
	return m.nativeBackgroundBindings().Emit(scope, event)
}
