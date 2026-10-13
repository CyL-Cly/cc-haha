package sdkhost

import (
	"context"
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (h *Host) nativeToolHookContext(scope *cooperative.Scope, call queryloop.JSONToolCallContext, makeContext func(queryloop.Round) queryhooks.Context) queryhooks.Context {
	c := makeContext(queryloop.Round{ToolContext: call.Messages})
	c.Scope = scope
	c.ToolUseContext = call.Opaque
	return c
}

func nativeHookToolID(raw json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(units); i++ {
		if units[i] >= 0xD800 && units[i] <= 0xDBFF {
			if i+1 == len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return "", cap("native hook unpaired UTF-16 tool identity")
			}
			i++
		} else if units[i] >= 0xDC00 && units[i] <= 0xDFFF {
			return "", cap("native hook unpaired UTF-16 tool identity")
		}
	}
	return string(utf16.Decode(units)), nil
}

func (h *Host) nativeHookAttachment(kind, event, name string, id json.RawMessage, fields ...protocol.JSONField) queryloop.Message {
	a := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(kind)})
	contentFirst := kind == "hook_system_message" || kind == "hook_additional_context" || kind == "hook_stopped_continuation" || kind == "hook_error_during_execution"
	appendFields := func() {
		for _, f := range fields {
			a.Set(f.Name, f.Value)
		}
	}
	if contentFirst {
		appendFields()
	}
	a.Set("hookName", quote(name))
	a.Set("toolUseID", id)
	a.Set("hookEvent", quote(event))
	if !contentFirst {
		appendFields()
	}
	raw, _ := a.MarshalJSON()
	return queryloop.Message{Record: h.options.Factory.CreateAttachment(raw)}
}

func hookRawTruthy(raw json.RawMessage) bool {
	return raw != nil && string(raw) != "null" && string(raw) != "false" && string(raw) != `""` && string(raw) != "0" && string(raw) != "-0"
}

func hookJoin(parts ...json.RawMessage) (json.RawMessage, error) {
	return protocol.JoinJavaScriptValuesJSON(parts, "")
}

func (h *Host) nativePreToolHooks(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext, makeContext func(queryloop.Round) queryhooks.Context, progress func(queryloop.Message) error) (queryloop.PreToolEffects, error) {
	result := queryloop.PreToolEffects{Input: in}
	c := h.nativeToolHookContext(scope, call, makeContext)
	sid := h.options.Controller.SessionID()
	if c.AgentID != "" {
		sid = c.AgentID
	}
	has, err := h.hooks.NativeEventPresent("PreToolUse", sid)
	if err != nil || !has {
		return result, err
	}
	id, err := nativeHookToolID(call.ToolUseID)
	if err != nil {
		return result, err
	}
	name := "PreToolUse:" + tool.Name
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quote(tool.Name)}, protocol.JSONField{Name: "tool_input", Value: raw(*in)}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID})
	var prevent bool
	var reason json.RawMessage
	err = h.hooks.ExecuteCommandEvent(ctx, queryhooks.CommandEvent{Event: "PreToolUse", SessionID: sid, ToolUseID: id, MatchQuery: tool.Name, Fields: fields, Call: c, IfMatcher: nativeHookIfMatcher(tool, in)}, func(value queryhooks.EventResult) error {
		if result.Stop {
			return nil
		}
		f := value.Fields
		if hookRawTruthy(f.Get("preventContinuation")) {
			prevent = true
			if hookRawTruthy(f.Get("stopReason")) {
				reason = f.Get("stopReason")
			}
		}
		if blocked, ok := f.Object("blockingError"); ok {
			message, e := hookJoin(quote(name+" hook error: "), blocked.Get("blockingError"))
			if e != nil {
				return e
			}
			result.Permission = &queryloop.ToolPermission{Behavior: "deny", Input: result.Input, Message: message, DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("hook")}, protocol.JSONField{Name: "hookName", Value: quote(name)}, protocol.JSONField{Name: "reason", Value: message}))}
		}
		if value.Message != nil {
			if value.Message.Type() == "progress" {
				return progress(*value.Message)
			}
			result.Messages = append(result.Messages, *value.Message)
		}
		if text := f.Get("systemMessage"); hookRawTruthy(text) {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_system_message", "PreToolUse", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: text}))
		}
		if text := f.Get("additionalContext"); hookRawTruthy(text) {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_additional_context", "PreToolUse", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: raw([]json.RawMessage{text})}))
		}
		if behavior := f.String("permissionBehavior"); behavior != "" {
			d := &queryloop.ToolPermission{Behavior: behavior, Input: result.Input, DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("hook")}, protocol.JSONField{Name: "hookName", Value: quote(name)}, protocol.JSONField{Name: "hookSource", Value: f.Get("hookSource")}, protocol.JSONField{Name: "reason", Value: f.Get("hookPermissionDecisionReason")}))}
			if behavior == "allow" || behavior == "ask" {
				if changed, ok := f.Object("updatedInput"); ok {
					d.UpdatedInput = &changed
				}
			}
			if behavior != "allow" {
				d.Message = f.Get("hookPermissionDecisionReason")
				if !hookRawTruthy(d.Message) {
					prose := "denied"
					if behavior == "ask" {
						prose = "asked for confirmation for"
					}
					d.Message = quote("Hook " + name + " " + prose + " this tool")
				}
			}
			result.Permission = d
		}
		if changed, ok := f.Object("passthroughUpdatedInput"); ok {
			result.Input = &changed
		}
		if ctx.Err() != nil {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_cancelled", "PreToolUse", name, call.ToolUseID))
			result.Stop = true
			receipt, err := hookJoin(quote("Error: "), reason)
			if err != nil {
				return err
			}
			if reason == nil {
				receipt = quote("Error: undefined")
			}
			block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "content", Value: quote("The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.")}, protocol.JSONField{Name: "is_error", Value: []byte("true")}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID})
			assistantID := ""
			if call.Invocation.Assistant != nil {
				assistantID = call.Invocation.Assistant.UUID
			}
			message := queryloop.CreateOrderedUserMessage(h.options.Factory, map[string]json.RawMessage{"content": raw([]protocol.JSONObject{block}), "toolUseResult": receipt, "sourceToolAssistantUUID": quote(assistantID)})
			result.StopMessage = &message
		}
		return nil
	})
	if err == nil && prevent {
		if !hookRawTruthy(reason) {
			reason = quote("Execution stopped by hook")
		}
		result.AfterMessages = append(result.AfterMessages, h.nativeHookAttachment("hook_stopped_continuation", "PreToolUse", name, call.ToolUseID, protocol.JSONField{Name: "message", Value: reason}))
	}
	return result, err
}

func (h *Host) nativePostToolHooks(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, output queryloop.ToolCallResult, call queryloop.JSONToolCallContext, makeContext func(queryloop.Round) queryhooks.Context, progress func(queryloop.Message) error) (queryloop.PostToolEffects, error) {
	result := queryloop.PostToolEffects{}
	c := h.nativeToolHookContext(scope, call, makeContext)
	sid := h.options.Controller.SessionID()
	if c.AgentID != "" {
		sid = c.AgentID
	}
	has, err := h.hooks.NativeEventPresent("PostToolUse", sid)
	if err != nil || !has {
		return result, err
	}
	id, err := nativeHookToolID(call.ToolUseID)
	if err != nil {
		return result, err
	}
	name := "PostToolUse:" + tool.Name
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quote(tool.Name)}, protocol.JSONField{Name: "tool_input", Value: raw(*in)}, protocol.JSONField{Name: "tool_response", Value: output.Data}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID})
	stopped := false
	err = h.hooks.ExecuteCommandEvent(ctx, queryhooks.CommandEvent{Event: "PostToolUse", SessionID: sid, ToolUseID: id, MatchQuery: tool.Name, Fields: fields, Call: c, IfMatcher: nativeHookIfMatcher(tool, in)}, func(value queryhooks.EventResult) error {
		if stopped {
			return nil
		}
		f := value.Fields
		if hookRawTruthy(f.Get("preventContinuation")) {
			reason := f.Get("stopReason")
			if !hookRawTruthy(reason) {
				reason = quote("Execution stopped by PostToolUse hook")
			}
			if blocked, ok := f.Object("blockingError"); ok {
				result.Messages = append(result.Messages, h.nativeHookAttachment("hook_blocking_error", "PostToolUse", name, call.ToolUseID, protocol.JSONField{Name: "blockingError", Value: raw(blocked)}))
			}
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_stopped_continuation", "PostToolUse", name, call.ToolUseID, protocol.JSONField{Name: "message", Value: reason}))
			stopped = true
			return nil
		}
		if value.Message != nil {
			if value.Message.Type() == "progress" {
				return progress(*value.Message)
			}
			a, _ := value.Message.Record.Object("attachment")
			if a.String("type") != "hook_blocking_error" {
				result.Messages = append(result.Messages, *value.Message)
			}
		}
		if blocked, ok := f.Object("blockingError"); ok {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_blocking_error", "PostToolUse", name, call.ToolUseID, protocol.JSONField{Name: "blockingError", Value: raw(blocked)}))
		}
		if text := f.Get("systemMessage"); hookRawTruthy(text) {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_system_message", "PostToolUse", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: text}))
		}
		if text := f.Get("additionalContext"); hookRawTruthy(text) {
			result.Messages = append(result.Messages, h.nativeHookAttachment("hook_additional_context", "PostToolUse", name, call.ToolUseID, protocol.JSONField{Name: "content", Value: raw([]json.RawMessage{text})}))
		}
		if updated := f.Get("updatedMCPToolOutput"); tool.IsMCP && hookRawTruthy(updated) {
			result.UpdatedOutput = updated
		}
		return nil
	})
	return result, err
}

func (h *Host) resolvePrintToolPermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	promptTool, err := h.resolveNativeMCPPermissionPrompt(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if err := h.captureSkillCallOverrides(call.Opaque); err != nil {
		return queryloop.ToolPermission{}, err
	}
	var forced *queryloop.ToolPermission
	if hook := call.HookPermission; hook != nil {
		if hook.Behavior == "deny" {
			d := *hook
			d.Input = in
			return d, nil
		}
		if hook.UpdatedInput != nil {
			in = hook.UpdatedInput
		}
		if hook.Behavior == "ask" {
			forced = hook
		}
		if hook.Behavior == "allow" {
			permissions, err := h.NativePermissionContext(scope)
			if err != nil {
				return queryloop.ToolPermission{}, err
			}
			interactive := tool.Name == "AskUserQuestion" || tool.Name == "ExitPlanMode" && h.options.Print.TeamWorker == nil
			if permissions.String("mode") != "auto" && (!interactive || hook.UpdatedInput != nil) {
				if h.options.Print.Tools == nil {
					return queryloop.ToolPermission{}, cap("actual Hook rule permission service")
				}
				rule, err := h.options.Print.Tools.CheckHookRulePermissions(scope, tool.Name, in)
				if err != nil {
					return queryloop.ToolPermission{}, err
				}
				if rule == nil {
					d := *hook
					d.Input = in
					return d, nil
				}
				if rule.Behavior == "deny" {
					return *rule, nil
				}
			}
		}
	}
	behavior, e := h.options.Print.Write.Permission(scope, *in)
	decision := queryloop.ToolPermission{Behavior: behavior, Input: in}
	if forced != nil {
		decision = *forced
		decision.Input = in
		behavior = decision.Behavior
		e = nil
	} else if b := h.options.Print.NativeSynthetic; b != nil && b.allows(h.options.Owner, tool) {
		decision = queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}
		behavior = "allow"
		e = nil
	} else if tool.IsMCP && h.options.Print.NativeMCP != nil {
		decision, e = h.options.Print.NativeMCP.Permission(scope, tool.Name, in)
		behavior = decision.Behavior
	} else if h.options.Print.Tools != nil {
		decision, e = h.options.Print.Tools.PermissionDecision(scope, tool.Name, in)
		behavior = decision.Behavior
	}
	if e == nil {
		e = scope.AwaitFulfilled()
	}
	if e == nil && h.options.Print.NativeProcess {
		decision, e = h.resolveNativeAutoPermission(ctx, scope, tool, in, call, decision)
		behavior = decision.Behavior
	}
	if e == nil && behavior == "ask" {
		permission, err := h.NativePermissionContext(scope)
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		if permission.String("mode") == "dontAsk" {
			return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote(messages.DontAskRejectMessage(tool.Name)), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("mode")}, protocol.JSONField{Name: "mode", Value: quote("dontAsk")}))}, nil
		}
		if promptTool != nil {
			call.CancelModel = h.nativeSDKPermissionAbort
			return h.callNativeMCPPermissionPrompt(ctx, scope, *promptTool, tool, in, call)
		}
		if h.options.Print.ResolvePermission == nil {
			if h.options.Print.NativeProcess {
				return decision, nil
			}
			return queryloop.ToolPermission{}, cap("actual permission ask resolver")
		}
		call.PermDecision = &decision
		call.NativePermissionHookSignal = h.nativePermissionHookSignal
		call.NativePermissionToolNames = []string{}
		for _, actual := range h.originalTools {
			call.NativePermissionToolNames = append(call.NativePermissionToolNames, actual.String("name"))
		}
		call.CancelModel = h.nativeSDKPermissionAbort
		return h.options.Print.ResolvePermission(ctx, scope, tool, in, call)
	}
	return decision, e
}
