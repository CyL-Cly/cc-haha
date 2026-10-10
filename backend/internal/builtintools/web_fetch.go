package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	weburl "github.com/nlnwa/whatwg-url/url"
	"strings"
	"time"
)

type WebFetchBindings struct {
	Fetch *webfetch.Service
	// Prepare captures session identity before native bytes I/O. The callback
	// must not touch Owner or mutable session state on its I/O worker.
	Prepare     func(*cooperative.Scope, context.Context) (context.Context, error)
	ApplyPrompt func(context.Context, *cooperative.Scope, json.RawMessage, bool) (json.RawMessage, error)
	Now         func() time.Time
}

func (s *Service) HasWebFetchBindings() bool {
	return s != nil && s.options.WebFetch != nil && s.options.WebFetch.Fetch != nil && s.options.WebFetch.ApplyPrompt != nil && s.options.WebFetch.Prepare != nil && s.options.WebFetch.Now != nil
}
func (s *Service) startWebFetch(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasWebFetchBindings() {
		return nil, capability("actual WebFetch producers")
	}
	url, e := text(in.Get("url"))
	if e != nil {
		return nil, e
	}
	ctx, e = s.options.WebFetch.Prepare(scope, ctx)
	if e != nil {
		return nil, e
	}
	start := s.options.WebFetch.Now()
	future, e := native(s, scope, func() (webfetch.Result, error) { return s.options.WebFetch.Fetch.Fetch(ctx, url) })
	if e != nil {
		return nil, e
	}
	return transform(s, scope, future, func(next *cooperative.Scope, response webfetch.Result) (queryloop.ToolCallResult, error) {
		output, e := s.options.WebFetch.Fetch.Process(ctx, url, in.Get("prompt"), s.options.NonInteractive, response, start, func(ctx context.Context, prompt json.RawMessage, nonInteractive bool) (json.RawMessage, error) {
			return s.options.WebFetch.ApplyPrompt(ctx, next, prompt, nonInteractive)
		})
		if e != nil {
			return queryloop.ToolCallResult{}, e
		}
		return queryloop.ToolCallResult{Data: raw(output)}, nil
	})
}
func validateWebFetch(in protocol.JSONObject) error {
	url, e := text(in.Get("url"))
	if e != nil {
		return e
	}
	if _, e = weburl.Parse(url); e != nil {
		return errors.New(fmt.Sprintf("Error: Invalid URL %q. The URL provided could not be parsed.", url))
	}
	return nil
}
func webFetchRule(in protocol.JSONObject) string {
	url, e := text(in.Get("url"))
	if e != nil {
		return "input:[object Object]"
	}
	u, e := weburl.Parse(url)
	if e != nil {
		return "input:[object Object]"
	}
	return "domain:" + u.Hostname()
}
func webRuleDecision(c protocol.JSONObject, name, content string) (string, json.RawMessage) {
	for _, row := range []struct{ Key, Behavior string }{{"alwaysDenyRules", "deny"}, {"alwaysAskRules", "ask"}, {"alwaysAllowRules", "allow"}} {
		var sources protocol.JSONObject
		_ = json.Unmarshal(c.Get(row.Key), &sources)
		// Source flattens rules in fixed settings-source order and Map keeps
		// the last matching contents. Retain the actual source provenance.
		var matched json.RawMessage
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
			var rules []string
			_ = json.Unmarshal(sources.Get(source), &rules)
			for _, rule := range rules {
				if rule == name+"("+content+")" {
					matched = raw(protocol.NewJSONObject(field("source", source), field("ruleValue", protocol.NewJSONObject(field("toolName", name), field("ruleContent", content))), field("ruleBehavior", row.Behavior)))
				}
			}
		}
		if matched != nil {
			return row.Behavior, raw(protocol.NewJSONObject(field("type", "rule"), protocol.JSONField{Name: "rule", Value: matched}))
		}
	}
	return "ask", nil
}
func webFetchToolDecision(c protocol.JSONObject, in *protocol.JSONObject) queryloop.ToolPermission {
	decision := queryloop.ToolPermission{Input: in}
	url, _ := text(in.Get("url"))
	description := "Claude wants to fetch content from this URL"
	if u, e := weburl.Parse(url); e == nil {
		description = "Claude wants to fetch content from " + u.Hostname()
		if webfetch.IsPreapprovedHost(u.Hostname(), u.Pathname()) {
			decision.Behavior = "allow"
			decision.UpdatedInput = in
			decision.DecisionReason = raw(protocol.NewJSONObject(field("type", "other"), field("reason", "Preapproved host")))
			decision.Description = quote(description)
			return decision
		}
	}
	decision.Description = quote(description)
	content := webFetchRule(*in)
	decision.Behavior, decision.DecisionReason = webRuleDecision(c, "WebFetch", content)
	if decision.Behavior == "deny" {
		decision.Message = quote("WebFetch denied access to " + content + ".")
	}
	if decision.Behavior == "ask" {
		decision.Message = quote("Claude requested permissions to use WebFetch, but you haven't granted it yet.")
		decision.Suggestions = raw([]protocol.JSONObject{protocol.NewJSONObject(field("type", "addRules"), field("destination", "localSettings"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "WebFetch"), field("ruleContent", content))}), field("behavior", "allow"))})
	}
	if decision.Behavior == "allow" {
		decision.UpdatedInput = in
	}
	return decision
}
func (s *Service) webPermissionDecision(scope *cooperative.Scope, name string, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	if wholeDenied(c, name) {
		return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote("Permission to use " + name + " has been denied.")}, nil
	}
	bypass := c.String("mode") == "bypassPermissions" || c.String("mode") == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	if !bypass {
		for _, list := range rules(c, "alwaysAskRules") {
			for _, rule := range list {
				if rule == name || rule == name+"(*)" {
					decision := webFetchToolDecision(c, in)
					if name == "WebSearch" {
						decision = webSearchDecision(in)
					}
					decision.Behavior = "ask"
					decision.UpdatedInput = nil
					return decision, nil
				}
			}
		}
	}
	if bypass || wholeAllowed(c, name) {
		return queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}, nil
	}
	if name == "WebFetch" {
		return webFetchToolDecision(c, in), nil
	}
	return webSearchDecision(in), nil
}
func isWebTool(name string) bool {
	return name == "WebFetch" || strings.EqualFold(name, "WebSearch") && name == "WebSearch"
}
