package builtintools

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type MCPResourceBindings struct {
	Available func(*cooperative.Scope) (bool, error)
	List      func(context.Context, *cooperative.Scope, *string) (json.RawMessage, error)
	Read      func(context.Context, *cooperative.Scope, string, string) (json.RawMessage, error)
}

func mcpResourceTool(name string) bool {
	return name == "ListMcpResourcesTool" || name == "ReadMcpResourceTool"
}
func (s *Service) HasMCPResourceBindings() bool {
	b := s.options.MCPResources
	return b != nil && b.Available != nil && b.List != nil && b.Read != nil
}

func (s *Service) mcpResourcePermission(scope *cooperative.Scope, name string) (string, error) {
	in := protocol.NewJSONObject()
	decision, e := s.mcpResourcePermissionDecision(scope, name, &in)
	return decision.Behavior, e
}

func resourceWholeRule(c protocol.JSONObject, key, behavior, name string) json.RawMessage {
	var sources protocol.JSONObject
	_ = sources.UnmarshalJSON(c.Get(key))
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
		var values []string
		_ = json.Unmarshal(sources.Get(source), &values)
		for _, value := range values {
			tool, _, content := skillParsedRule(value)
			if tool == name && !content {
				return raw(protocol.NewJSONObject(field("type", "rule"), field("rule", protocol.NewJSONObject(field("source", source), field("ruleBehavior", behavior), field("ruleValue", protocol.NewJSONObject(field("toolName", name)))))))
			}
		}
	}
	return nil
}

func (s *Service) mcpResourcePermissionDecision(scope *cooperative.Scope, name string, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	decision := queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}
	if reason := resourceWholeRule(c, "alwaysDenyRules", "deny", name); reason != nil {
		decision.Behavior, decision.UpdatedInput = "deny", nil
		decision.Message = quote("Permission to use " + name + " has been denied.")
		decision.DecisionReason = reason
		return decision, nil
	}
	bypass := c.String("mode") == "bypassPermissions" || c.String("mode") == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	if !bypass {
		if reason := resourceWholeRule(c, "alwaysAskRules", "ask", name); reason != nil {
			decision.Behavior, decision.UpdatedInput = "ask", nil
			decision.Message = quote("Claude requested permissions to use " + name + ", but you haven't granted it yet.")
			decision.DecisionReason = reason
			if c.String("mode") == "dontAsk" {
				decision.Behavior = "deny"
				decision.DecisionReason = raw(protocol.NewJSONObject(field("type", "mode"), field("mode", "dontAsk")))
				decision.Message = quote("Permission to use " + name + " has been denied because Claude Code is running in don't ask mode. IMPORTANT: You *may* attempt to accomplish this action using other tools that might naturally be used to accomplish this goal, e.g. using head instead of cat. But you *should not* attempt to work around this denial in malicious ways, e.g. do not use your ability to run tests to execute non-test actions. You should only try to work around this restriction in reasonable ways that do not attempt to bypass the intent behind this denial. If you believe this capability is essential to complete the user's request, STOP and explain to the user what you were trying to do and why you need this permission. Let the user decide how to proceed.")
			}
			return decision, nil
		}
	}
	if bypass {
		decision.DecisionReason = raw(protocol.NewJSONObject(field("type", "mode"), field("mode", c.String("mode"))))
	} else {
		decision.DecisionReason = resourceWholeRule(c, "alwaysAllowRules", "allow", name)
	}
	// The original buildTool checkPermissions defaults to allow. These tools
	// are read-only and have no filesystem or resource-specific permission rule.
	return decision, nil
}
func (s *Service) callMCPResource(ctx context.Context, scope *cooperative.Scope, name string, in protocol.JSONObject) (json.RawMessage, error) {
	if !s.HasMCPResourceBindings() {
		return nil, capability("actual connected MCP resource bindings")
	}
	if name == "ListMcpResourcesTool" {
		var server *string
		if in.Get("server") != nil {
			value, e := text(in.Get("server"))
			if e != nil {
				return nil, e
			}
			server = &value
		}
		return s.options.MCPResources.List(ctx, scope, server)
	}
	server, e := text(in.Get("server"))
	if e != nil {
		return nil, e
	}
	uri, e := text(in.Get("uri"))
	if e != nil {
		return nil, e
	}
	return s.options.MCPResources.Read(ctx, scope, server, uri)
}

func (s *Service) startMCPResource(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if e != nil {
		return nil, e
	}
	receipt, e := s.options.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
		data, e := s.callMCPResource(ctx, next, name, in)
		if e != nil {
			return future.Reject(next, e)
		}
		return future.Resolve(next, queryloop.ToolCallResult{Data: data})
	})
	if e != nil {
		_ = future.RejectAdministrative(e)
		return nil, e
	}
	// Receipt observation is administrative only. Cancellation can reject an
	// admitted task before its business callback executes; settle that pending
	// promise rather than leaving a readonly batch waiting indefinitely.
	go func() {
		if e := receipt.Wait(); e != nil {
			_ = future.RejectAdministrative(e)
		}
	}()
	return future, nil
}
func mapMCPResourceResult(name string, data, id json.RawMessage) (protocol.JSONObject, error) {
	if name == "ListMcpResourcesTool" && (len(data) == 0 || string(data) == "null" || string(data) == "[]") {
		return textResult(id, "No resources found. MCP servers may still provide tools even if they have no resources."), nil
	}
	value, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return textResult(id, string(value)), nil
}
