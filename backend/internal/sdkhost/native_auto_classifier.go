package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/automode"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"strings"
)

func (h *Host) nativeAutoSnapshot() (automode.Snapshot, error) {
	sources := map[string]json.RawMessage{}
	for _, name := range []string{"userSettings", "localSettings", "flagSettings", "policySettings"} {
		o, e := h.options.Startup.GetSettingsForSource(name)
		if e != nil {
			return automode.Snapshot{}, e
		}
		if o != nil {
			sources[name], e = o.MarshalJSON()
			if e != nil {
				return automode.Snapshot{}, e
			}
		}
	}
	return automode.Snapshot{TranscriptClassifier: true, Sources: sources}, nil
}
func autoSafeTool(name string) bool {
	switch name {
	case "Read", "Grep", "Glob", "LSP", "ToolSearch", "ListMcpResources", "ReadMcpResourceTool", "TodoWrite", "TaskCreate", "TaskGet", "TaskUpdate", "TaskList", "TaskStop", "TaskOutput", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "TeamCreate", "TeamPlan", "TeamDelete", "SendMessage", "Workflow", "Sleep", "classify_result":
		return true
	}
	return false
}
func (h *Host) resolveNativeAutoPermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext, original queryloop.ToolPermission) (queryloop.ToolPermission, error) {
	permissions, e := h.NativePermissionContext(scope)
	if e != nil {
		return original, e
	}
	active, e := h.options.Controller.NativeAutoModeActive(scope)
	if e != nil {
		return original, e
	}
	if permissions.String("mode") != "auto" && (permissions.String("mode") != "plan" || !active) {
		return original, nil
	}
	snapshot, e := h.nativeAutoSnapshot()
	if e != nil {
		return original, e
	}
	config := automode.MergeTrusted(snapshot)
	force := config != nil && config.ClassifyAllShell != nil && *config.ClassifyAllShell && (tool.Name == "Bash" || tool.Name == "PowerShell")
	input := in
	if original.UpdatedInput != nil {
		input = original.UpdatedInput
	}
	if force && original.Behavior == "allow" {
		original.Behavior = "ask"
		original.Message = quote("Claude requested permissions to use " + tool.Name + ", but you haven't granted it yet.")
	}
	if original.Behavior == "allow" {
		if permissions.String("mode") == "auto" {
			h.autoConsecutiveDenials = 0
		}
		return original, nil
	}
	if original.Behavior != "ask" {
		return original, nil
	}
	var reason protocol.JSONObject
	_ = reason.UnmarshalJSON(original.DecisionReason)
	avoid := string(permissions.Get("shouldAvoidPermissionPrompts")) == "true"
	if reason.String("type") == "safetyCheck" && string(reason.Get("classifierApprovable")) != "true" {
		if avoid {
			return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: original.Message, DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("asyncAgent")}, protocol.JSONField{Name: "reason", Value: quote("Safety check requires interactive approval and permission prompts are not available in this context")}))}, nil
		}
		return original, nil
	}
	if tool.Name == "AskUserQuestion" || tool.Name == "ExitPlanMode" {
		return original, nil
	}
	if tool.Name == "PowerShell" && !force {
		if avoid {
			return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote("PowerShell tool requires interactive approval")}, nil
		}
		return original, nil
	}
	allow := func(input *protocol.JSONObject, why json.RawMessage) queryloop.ToolPermission {
		h.autoConsecutiveDenials = 0
		return queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: input, DecisionReason: why}
	}
	if tool.Name != "Agent" && tool.Name != "REPL" && !force && h.options.Print.Tools != nil {
		view, e := h.options.Print.Tools.PermissionView(scope, func(s *cooperative.Scope) (protocol.JSONObject, error) {
			c, e := h.NativePermissionContext(s)
			if e == nil {
				c.Set("mode", quote("acceptEdits"))
			}
			return c, e
		})
		if e != nil {
			return original, e
		}
		parsed, e := tool.ParseInput(*input)
		if e == nil {
			decision, e := view.PermissionDecision(scope, tool.Name, &parsed)
			if e == nil {
				if e = scope.AwaitFulfilled(); e != nil {
					return original, e
				}
				if decision.Behavior == "allow" {
					changed := decision.UpdatedInput
					if changed == nil {
						changed = in
					}
					return allow(changed, raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("mode")}, protocol.JSONField{Name: "mode", Value: quote("auto")}))), nil
				}
			} else if errors.Is(e, context.Canceled) {
				return original, e
			}
		}
	}
	if autoSafeTool(tool.Name) {
		return allow(input, raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("mode")}, protocol.JSONField{Name: "mode", Value: quote("auto")}))), nil
	}
	model, e := h.InferenceModel(scope)
	if e != nil {
		return original, e
	}
	names := []string{}
	for _, t := range h.registered {
		names = append(names, t.Name)
		names = append(names, t.Aliases...)
	}
	history := []protocol.JSONObject{}
	for _, m := range call.Messages {
		bytes, e := m.MarshalJSON()
		if e != nil {
			return original, e
		}
		var r protocol.JSONObject
		if e = r.UnmarshalJSON(bytes); e != nil {
			return original, e
		}
		history = append(history, r)
	}
	memory, e := h.options.Controller.NativeClassifierClaudeMD(scope)
	if e != nil {
		return original, e
	}
	opts, e := automode.ClassifierOptions(history, tool.Name, raw(*input), names, snapshot, memory)
	if e != nil {
		return original, e
	}
	if len(opts.Messages) == 0 {
		return allow(input, raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("classifier")}, protocol.JSONField{Name: "classifier", Value: quote("auto-mode")}, protocol.JSONField{Name: "reason", Value: quote("Tool declares no classifier-relevant input")}))), nil
	}
	var classification automode.Classification
	e = h.nativeAutoModeModels.Run(scope, ctx, func(producer *cooperative.Scope) error {
		device, e := h.requestDeviceID(producer)
		if e != nil {
			return e
		}
		settings, e := h.options.Startup.GetInitialSettings()
		if e != nil {
			return e
		}
		input := requestretry.Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: model, QuerySource: "auto_mode"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: device, SessionID: h.options.Controller.SessionID()}}, Runtime: h.options.Runtime}
		response, e := requestretry.RunNativeAutoModeQuery(ctx, input, *settings, opts, h.requestDependencies(producer), h.nativeAutoModeModels)
		if e != nil {
			return e
		}
		classification = automode.ParseClassification(response.Raw)
		return producer.AwaitFulfilled()
	})
	if e != nil || classification.Unavailable {
		if provider.IsOwnerWaitFailure(e) {
			return original, e
		}
		if ctx.Err() != nil {
			return original, context.Cause(ctx)
		}
		if e != nil && strings.Contains(strings.ToLower(e.Error()), "prompt is too long") {
			if avoid {
				return original, &NativeSDKCallbackError{Name: "AbortError", Message: "Agent aborted: auto mode classifier transcript exceeded context window in headless mode"}
			}
			original.DecisionReason = raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("other")}, protocol.JSONField{Name: "reason", Value: quote("Auto mode classifier transcript exceeded context window — falling back to manual approval")}))
			return original, nil
		}
		if avoid {
			message := model + " is temporarily unavailable, so auto mode cannot determine the safety of " + tool.Name + " right now. Wait briefly and then try this action again. If it keeps failing, continue with other tasks that don't require this action and come back to it later. Note: reading files, searching code, and other read-only operations do not require the classifier and can still be used."
			return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote(message), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("classifier")}, protocol.JSONField{Name: "classifier", Value: quote("auto-mode")}, protocol.JSONField{Name: "reason", Value: quote("Classifier unavailable")}))}, nil
		}
		return original, nil
	}
	reasonJSON := classification.ReasonJSON
	if reasonJSON == nil {
		reasonJSON = quote(classification.Reason)
	}
	why := raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("classifier")}, protocol.JSONField{Name: "classifier", Value: quote("auto-mode")}, protocol.JSONField{Name: "reason", Value: reasonJSON}))
	if !classification.ShouldBlock {
		return allow(input, why), nil
	}
	h.autoConsecutiveDenials++
	h.autoTotalDenials++
	if h.autoConsecutiveDenials >= 3 || h.autoTotalDenials >= 20 {
		if avoid {
			return original, &NativeSDKCallbackError{Name: "AbortError", Message: "Agent aborted: too many classifier denials in headless mode"}
		}
		if h.autoTotalDenials >= 20 {
			h.autoConsecutiveDenials = 0
			h.autoTotalDenials = 0
		}
		return original, nil
	}
	messageJSON, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Permission for this action has been denied. Reason: "), reasonJSON, quote(". If you have other tasks that don't depend on this action, continue working on those. IMPORTANT: You *may* attempt to accomplish this action using other tools that might naturally be used to accomplish this goal, e.g. using head instead of cat. But you *should not* attempt to work around this denial in malicious ways, e.g. do not use your ability to run tests to execute non-test actions. You should only try to work around this restriction in reasonable ways that do not attempt to bypass the intent behind this denial. If you believe this capability is essential to complete the user's request, STOP and explain to the user what you were trying to do and why you need this permission. Let the user decide how to proceed. To allow this type of action in the future, the user can add a Bash permission rule to their settings.")}, "")
	if e != nil {
		return original, e
	}
	return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: messageJSON, DecisionReason: why}, nil
}
