package builtintools

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type WorkflowBindings struct {
	Enabled     func(*cooperative.Scope) (bool, error)
	Launch      func(context.Context, *cooperative.Scope, protocol.JSONObject, json.RawMessage) (protocol.JSONObject, error)
	Guideline   func(*cooperative.Scope) (string, error)
	AutoConsent func(*cooperative.Scope) (bool, error)
	Ultracode   func(*cooperative.Scope) (bool, error)
}

func (s *Service) HasWorkflowBindings() bool {
	b := s.options.Workflow
	return b != nil && b.Enabled != nil && b.Launch != nil && b.Guideline != nil && b.AutoConsent != nil && b.Ultracode != nil
}
func (s *Service) callWorkflow(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	if !s.HasWorkflowBindings() {
		return queryloop.ToolCallResult{}, capability("actual Workflow shared orchestration/task/Agent/journal producer")
	}
	out, e := s.options.Workflow.Launch(ctx, scope, in, call.ToolUseID)
	data, _ := out.MarshalJSON()
	return queryloop.ToolCallResult{Data: data}, e
}
func workflowNamedRule(c protocol.JSONObject, key, behavior, name string) json.RawMessage {
	sources, _ := c.Object(key)
	var found json.RawMessage
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
		var rules []string
		_ = json.Unmarshal(sources.Get(source), &rules)
		for _, text := range rules {
			tool, content, has := skillParsedRule(text)
			if tool == "Workflow" && has && content == name {
				rule := protocol.NewJSONObject(field("source", source), field("ruleBehavior", behavior), field("ruleValue", protocol.NewJSONObject(field("toolName", "Workflow"), field("ruleContent", content))))
				found = raw(protocol.NewJSONObject(field("type", "rule"), protocol.JSONField{Name: "rule", Value: raw(rule)}))
			}
		}
	}
	return found
}
func (s *Service) workflowPermission(scope *cooperative.Scope, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	decision := queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}
	if c.String("mode") == "bypassPermissions" || s.options.NonInteractive {
		return decision, nil
	}
	if !s.HasWorkflowBindings() {
		return decision, capability("actual Workflow authorization producers")
	}
	ultra, e := s.options.Workflow.Ultracode(scope)
	if e != nil {
		return decision, e
	}
	if ultra {
		return decision, nil
	}
	if c.String("mode") == "auto" {
		consent, e := s.options.Workflow.AutoConsent(scope)
		if e != nil {
			return decision, e
		}
		if consent {
			return decision, nil
		}
	}
	name := in.String("name")
	if name != "" {
		if reason := workflowNamedRule(c, "alwaysDenyRules", "deny", name); reason != nil {
			decision.Behavior = "deny"
			decision.UpdatedInput = nil
			decision.Message = quote("Workflow denied for workflow \"" + name + "\".")
			decision.DecisionReason = reason
			return decision, nil
		}
		if reason := workflowNamedRule(c, "alwaysAllowRules", "allow", name); reason != nil {
			decision.DecisionReason = reason
			return decision, nil
		}
	}
	decision.Behavior = "ask"
	decision.UpdatedInput = nil
	decision.Message = quote("Claude wants to run a dynamic workflow, which can spawn many subagents and use a large number of tokens.")
	if name != "" {
		decision.Suggestions = raw([]protocol.JSONObject{protocol.NewJSONObject(field("type", "addRules"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "Workflow"), field("ruleContent", name))}), field("behavior", "allow"), field("destination", "localSettings"))})
	}
	return decision, nil
}

func mapWorkflowResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	text, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", string(text))), nil
}
