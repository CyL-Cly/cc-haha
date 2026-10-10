package builtintools

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func (s *Service) sandboxAutoPermission(scope *cooperative.Scope, in *protocol.JSONObject) (*queryloop.ToolPermission, error) {
	if !s.HasSandboxBindings() {
		return nil, nil
	}
	if e := s.check(scope); e != nil {
		return nil, e
	}
	enabled, e := s.options.BashSandbox.AutoAllow(scope, *in)
	if e != nil || !enabled {
		return nil, e
	}
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	// Whole-tool rules are handled by the outer permission stage in Source.
	if wholeDenied(c, "Bash") {
		return nil, nil
	}
	for _, list := range rules(c, "alwaysAskRules") {
		for _, rule := range list {
			if rule == "Bash" || rule == "Bash(*)" {
				return nil, nil
			}
		}
	}
	return sandboxAutoPermissionForContext(*in, c, s.options.Platform)
}
func sandboxAutoPermissionForContext(in, context protocol.JSONObject, platform string) (*queryloop.ToolPermission, error) {
	classifier, e := sessionrewind.NewRecordedCommandClassifier(platform, "external")
	if e != nil {
		return nil, e
	}
	command := strings.TrimFunc(in.String("command"), bashSpace)
	rows := map[string][]hookRule{}
	for _, behavior := range []string{"deny", "ask"} {
		all, e := orderedHookRules(context, behavior)
		if e != nil {
			return nil, e
		}
		index := map[string]int{}
		kept := []hookRule{}
		for _, rule := range all {
			if rule.value.String("toolName") != "Bash" || rule.value.Get("ruleContent") == nil {
				continue
			}
			key := string(rule.value.Get("ruleContent"))
			if old, ok := index[key]; ok {
				kept[old] = rule
			} else {
				index[key] = len(kept)
				kept = append(kept, rule)
			}
		}
		rows[behavior] = kept
	}
	find := func(value, behavior string) (*hookRule, error) {
		projection, e := classifier.ExtractShellOutputRedirections(strings.TrimFunc(value, bashSpace))
		if e != nil {
			return nil, e
		}
		candidates := sandboxruntime.RuleCandidates(projection.CommandWithoutRedirections)
		for _, rule := range rows[behavior] {
			for _, candidate := range candidates {
				matched, e := sandboxruntime.MatchCommandRule(rule.value.String("ruleContent"), candidate)
				if e != nil {
					return nil, e
				}
				if matched {
					copy := rule
					return &copy, nil
				}
			}
		}
		return nil, nil
	}
	deny := func(rule *hookRule) *queryloop.ToolPermission {
		return &queryloop.ToolPermission{Behavior: "deny", Input: &in, Message: quote("Permission to use Bash with command " + command + " has been denied."), DecisionReason: rule.reason()}
	}
	ask := func(rule *hookRule) *queryloop.ToolPermission {
		return &queryloop.ToolPermission{Behavior: "ask", Input: &in, Message: quote("Claude requested permissions to use Bash, but you haven't granted it yet."), DecisionReason: rule.reason()}
	}
	fullDeny, e := find(command, "deny")
	if e != nil {
		return nil, e
	}
	if fullDeny != nil {
		return deny(fullDeny), nil
	}
	fullAsk, e := find(command, "ask")
	if e != nil {
		return nil, e
	}
	parts, e := classifier.SplitForSandboxPermission(command)
	if e != nil {
		return nil, e
	}
	var firstAsk *hookRule
	if len(parts) > 1 {
		for _, part := range parts {
			d, e := find(part, "deny")
			if e != nil {
				return nil, e
			}
			if d != nil {
				return deny(d), nil
			}
			a, e := find(part, "ask")
			if e != nil {
				return nil, e
			}
			if firstAsk == nil {
				firstAsk = a
			}
		}
		if firstAsk != nil {
			return ask(firstAsk), nil
		}
	}
	if fullAsk != nil {
		return ask(fullAsk), nil
	}
	return &queryloop.ToolPermission{Behavior: "allow", Input: &in, UpdatedInput: &in, DecisionReason: raw(protocol.NewJSONObject(field("type", "other"), field("reason", "Auto-allowed with sandbox (autoAllowBashIfSandboxed enabled)")))}, nil
}
