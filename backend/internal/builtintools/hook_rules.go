package builtintools

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"strings"
)

var hookRuleSources = []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"}

type hookRule struct {
	value            protocol.JSONObject
	source, behavior string
	raw              string
}

func orderedHookRules(c protocol.JSONObject, behavior string) ([]hookRule, error) {
	key := "alwaysDenyRules"
	if behavior == "ask" {
		key = "alwaysAskRules"
	}
	if behavior == "allow" {
		key = "alwaysAllowRules"
	}
	values, _ := c.Object(key)
	out := []hookRule{}
	for _, source := range hookRuleSources {
		var rows []json.RawMessage
		if raw := values.Get(source); raw != nil {
			if e := json.Unmarshal(raw, &rows); e != nil {
				return nil, e
			}
		}
		for _, row := range rows {
			value, e := sdkpermissions.ParsePermissionRuleValue(row)
			if e != nil {
				return nil, e
			}
			var text string
			_ = json.Unmarshal(row, &text)
			out = append(out, hookRule{value, source, behavior, text})
		}
	}
	return out, nil
}
func (r hookRule) reason() json.RawMessage {
	return raw(protocol.NewJSONObject(field("type", "rule"), field("rule", protocol.NewJSONObject(field("source", r.source), field("ruleBehavior", r.behavior), field("ruleValue", r.value)))))
}
func hookToolRuleMatches(name string, r hookRule) bool {
	if r.value.Get("ruleContent") != nil {
		return false
	}
	rule := r.value.String("toolName")
	if rule == name {
		return true
	}
	if strings.HasPrefix(rule, "mcp__") && strings.HasPrefix(name, "mcp__") {
		server := strings.TrimPrefix(rule, "mcp__")
		if strings.HasSuffix(server, "__*") {
			server = strings.TrimSuffix(server, "__*")
		}
		if !strings.Contains(server, "__") {
			return strings.HasPrefix(name, "mcp__"+server+"__")
		}
	}
	return false
}

// CheckHookRulePermissions is the original rule-only permission stage. Hook
// approval cannot bypass a matching deny/ask rule or a tool safety objection.
// Its nil result means no rule objection; it does not grant or persist a rule.
func (s *Service) CheckHookRulePermissions(scope *cooperative.Scope, name string, in *protocol.JSONObject) (*queryloop.ToolPermission, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if in == nil || !in.IsObject() {
		return nil, capability("actual parsed Hook permission input")
	}
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	bypass := c.String("mode") == "bypassPermissions" || c.String("mode") == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	for _, behavior := range []string{"deny", "ask"} {
		rows, e := orderedHookRules(c, behavior)
		if e != nil {
			return nil, e
		}
		for _, rule := range rows {
			if !hookToolRuleMatches(name, rule) || behavior == "ask" && bypass {
				continue
			}
			message := "Permission to use " + name + " has been denied."
			if behavior == "ask" {
				message = "Claude requested permissions to use " + name + ", but you haven't granted it yet."
			}
			return &queryloop.ToolPermission{Behavior: behavior, Input: in, Message: quote(message), DecisionReason: rule.reason()}, nil
		}
	}
	file := name == "Read" || name == "Glob" || name == "Grep" || name == "Write" || name == "Edit" || name == "NotebookEdit"
	if file {
		path, e := s.toolPath(name, *in)
		if e != nil {
			return nil, e
		}
		forms := []string{path}
		if canonical := resolved(path); canonical != path {
			forms = append(forms, canonical)
		}
		ruleName, action := "Edit", "edit"
		if name == "Read" || name == "Glob" || name == "Grep" {
			ruleName, action = "Read", "read"
		}
		find := func(behavior string) (*hookRule, error) {
			for _, form := range forms {
				r, e := s.matchingHookFileRule(scope, c, ruleName, behavior, form)
				if e != nil {
					return nil, e
				}
				if r != nil {
					return r, nil
				}
			}
			return nil, nil
		}
		decision := func(behavior string, r *hookRule) *queryloop.ToolPermission {
			message := "Permission to " + action + " " + path + " has been denied."
			if behavior == "ask" {
				message = "Claude requested permissions to read from " + path + ", but you haven't granted it yet."
				if ruleName == "Edit" {
					message = "Claude requested permissions to write to " + path + ", but you haven't granted it yet."
				}
			}
			return &queryloop.ToolPermission{Behavior: behavior, Input: in, Message: quote(message), DecisionReason: r.reason()}
		}
		denied, e := find("deny")
		if e != nil {
			return nil, e
		}
		if denied != nil {
			return decision("deny", denied), nil
		}
		if ruleName == "Read" {
			asked, e := find("ask")
			if e != nil {
				return nil, e
			}
			if asked != nil && !bypass {
				return decision("ask", asked), nil
			}
			return nil, nil
		}
		if s.options.HookInternalEditable == nil || s.options.HookPathSafety == nil {
			return nil, capability("actual Hook path safety/internal native producer")
		}
		allowed, e := s.options.HookInternalEditable(scope, path)
		if e != nil {
			return nil, e
		}
		if allowed {
			return nil, nil
		}
		session := c.Clone()
		sessionRules, _ := c.Object("alwaysAllowRules")
		session.Set("alwaysAllowRules", raw(protocol.NewJSONObject(protocol.JSONField{Name: "session", Value: sessionRules.Get("session")})))
		scoped, e := s.matchingHookFileRule(scope, session, ruleName, "allow", path)
		if e != nil {
			return nil, e
		}
		if scoped != nil {
			content := scoped.value.String("ruleContent")
			if (strings.HasPrefix(content, "/.claude/") || strings.HasPrefix(content, "~/.claude/")) && !strings.Contains(content, "..") && strings.HasSuffix(content, "/**") {
				return nil, nil
			}
		}
		safetyContext, e := s.options.HookPathSafety(scope)
		if e != nil {
			return nil, e
		}
		safety := CheckHookPathSafety(path, forms, safetyContext, false)
		if !safety.Safe && !bypass {
			return &queryloop.ToolPermission{Behavior: "ask", Input: in, Message: quote(safety.Message), Suggestions: raw([]protocol.JSONObject{protocol.NewJSONObject(field("type", "setMode"), field("mode", "acceptEdits"), field("destination", "session"))}), DecisionReason: raw(protocol.NewJSONObject(field("type", "safetyCheck"), field("reason", safety.Message), field("classifierApprovable", safety.ClassifierApprovable)))}, nil
		}
		asked, e := find("ask")
		if e != nil {
			return nil, e
		}
		if asked != nil && !bypass {
			return decision("ask", asked), nil
		}
		return nil, nil
	}
	// Native MCP definitions are registered on the owning shared connection.
	// Their original checkPermissions is passthrough; whole-tool rules above
	// remain authoritative, and Hook allow is still handled by the caller.
	if strings.HasPrefix(name, "mcp__") {
		return nil, nil
	}
	decision, e := s.PermissionDecision(scope, name, in)
	if e != nil {
		return nil, e
	}
	if decision.Behavior == "deny" {
		return &decision, nil
	}
	reason := protocol.JSONObject{}
	_ = reason.UnmarshalJSON(decision.DecisionReason)
	if !bypass && decision.Behavior == "ask" && (reason.String("type") == "safetyCheck" || reason.String("type") == "rule") {
		return &decision, nil
	}
	return nil, nil
}
