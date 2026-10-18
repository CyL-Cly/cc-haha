package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

type nativeSDKPermissionState struct {
	open    bool
	pending map[string]*NativeSDKPermissionPrompt
}

// The stdio channel is selected by the public permission-prompt-tool option.
// Unselected print sessions retain the ordinary permission result.
func (h *Host) bindNativeSDKPermissionInput(scope *cooperative.Scope, input *PlainStructuredInput) error {
	if h.options.Print == nil || h.options.Print.PermissionPromptToolName != "stdio" {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	h.sdkPermissions = &nativeSDKPermissionState{open: true, pending: map[string]*NativeSDKPermissionPrompt{}}
	previousResponse, previousClose := input.sandboxResponse, input.sandboxClose
	input.sandboxResponse = func(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
		if handled, err := h.nativeSDKPermissionResponse(scope, message); handled || err != nil {
			return handled, err
		}
		if previousResponse != nil {
			return previousResponse(scope, message)
		}
		return false, nil
	}
	input.sandboxClose = func(scope *cooperative.Scope) error {
		if err := h.closeNativeSDKPermissionInput(scope); err != nil {
			return err
		}
		if previousClose != nil {
			return previousClose(scope)
		}
		return nil
	}
	return nil
}

func sdkPermissionOutput(value json.RawMessage) (protocol.JSONObject, error) {
	response := object(value)
	if response.IsObject() {
		if updates := response.Get("updatedPermissions"); updates != nil {
			parsed := sdkpermissions.ParsePermissionUpdates(updates)
			if parsed == nil {
				response.Set("updatedPermissions", nil)
			} else {
				response.Set("updatedPermissions", raw(parsed))
			}
		}
		if classification := response.Get("decisionClassification"); classification != nil {
			switch response.String("decisionClassification") {
			case "user_temporary", "user_permanent", "user_reject":
			default:
				response.Set("decisionClassification", nil)
			}
		}
		value = raw(response)
	}
	var schema pluginschema.Node
	err := json.Unmarshal([]byte(`{"type":"union","options":[{"type":"object","shape":{"behavior":{"type":"literal","values":["allow"]},"updatedInput":{"type":"record","keyType":{"type":"string"},"valueType":{"type":"unknown"}},"updatedPermissions":{"type":"optional","innerType":{"type":"unknown"}},"toolUseID":{"type":"optional","innerType":{"type":"string"}},"decisionClassification":{"type":"optional","innerType":{"type":"string"}}}},{"type":"object","shape":{"behavior":{"type":"literal","values":["deny"]},"message":{"type":"string"},"interrupt":{"type":"optional","innerType":{"type":"boolean"}},"toolUseID":{"type":"optional","innerType":{"type":"string"}},"decisionClassification":{"type":"optional","innerType":{"type":"string"}}}}]}`), &schema)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	parsed, issues := pluginschema.Validate(&schema, value)
	if len(issues) > 0 {
		return protocol.JSONObject{}, &NativeSDKCallbackError{Name: "ZodError", Message: pluginschema.ErrorJSON(issues, nil)}
	}
	return object(parsed), nil
}
func (h *Host) nativeSDKPermissionResponse(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	if h.sdkPermissions == nil {
		return false, nil
	}
	response := object(message.Get("response"))
	id := response.String("request_id")
	prompt := h.sdkPermissions.pending[id]
	if prompt == nil {
		return false, nil
	}
	if prompt.context.Err() != nil {
		return true, prompt.Cancel(scope)
	}
	delete(h.sdkPermissions.pending, id)
	h.forgetSDKPermissionRequest(id)
	if response.String("subtype") == "error" {
		return true, prompt.future.Reject(scope, errors.New(response.String("error")))
	}
	parsed, err := sdkPermissionOutput(response.Get("response"))
	if err != nil {
		return true, prompt.future.Reject(scope, err)
	}
	return true, prompt.future.Resolve(scope, parsed)
}
func (h *Host) closeNativeSDKPermissionInput(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if h.sdkPermissions == nil {
		return nil
	}
	h.sdkPermissions.open = false
	for id, prompt := range h.sdkPermissions.pending {
		// Source's AbortSignal listener runs before a later stream close.
		// Native context callbacks can still be waiting for Owner admission.
		if prompt.context.Err() != nil {
			if err := prompt.Cancel(scope); err != nil {
				return err
			}
			continue
		}
		delete(h.sdkPermissions.pending, id)
		h.forgetSDKPermissionRequest(id)
		if err := prompt.future.Reject(scope, errors.New("Tool permission stream closed before response received")); err != nil {
			return err
		}
	}
	return nil
}
func sdkPermissionErrorText(err error) string {
	var named *NativeSDKCallbackError
	if errors.As(err, &named) {
		if named.Name == "ZodError" {
			return named.Message
		}
		if named.Message == "" {
			return named.Name
		}
		return named.Name + ": " + named.Message
	}
	return "Error: " + err.Error()
}

type NativeSDKPermissionPrompt struct {
	host      *Host
	context   context.Context
	tool      queryloop.RegisteredTool
	input     *protocol.JSONObject
	call      queryloop.JSONToolCallContext
	requestID string
	future    *OwnerFuture[protocol.JSONObject]
	stop      func() bool
}

func (prompt *NativeSDKPermissionPrompt) RawFuture() *OwnerFuture[protocol.JSONObject] {
	return prompt.future
}
func (prompt *NativeSDKPermissionPrompt) failed(err error) (queryloop.ToolPermission, error) {
	result := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: quote("deny")}, protocol.JSONField{Name: "message", Value: quote("Tool permission request failed: " + sdkPermissionErrorText(err))}, protocol.JSONField{Name: "toolUseID", Value: prompt.call.ToolUseID})
	return queryloop.ToolPermission{Behavior: "deny", Input: prompt.input, Message: result.Get("message"), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("permissionPromptTool")}, protocol.JSONField{Name: "permissionPromptToolName", Value: quote(prompt.tool.Name)}, protocol.JSONField{Name: "toolResult", Value: raw(result)}))}, nil
}
func (prompt *NativeSDKPermissionPrompt) Complete(scope *cooperative.Scope, result protocol.JSONObject, err error) (queryloop.ToolPermission, error) {
	if errors.Is(err, cooperative.ErrClosed) {
		return queryloop.ToolPermission{}, err
	}
	if prompt == nil || !scope.BelongsTo(prompt.host.options.Owner) {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	if err != nil {
		if errors.Is(err, cooperative.ErrClosed) {
			return queryloop.ToolPermission{}, err
		}
		return prompt.failed(err)
	}
	decision := queryloop.ToolPermission{Behavior: result.String("behavior"), Input: prompt.input, Message: result.Get("message"), UpdatedPermissions: result.Get("updatedPermissions"), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("permissionPromptTool")}, protocol.JSONField{Name: "permissionPromptToolName", Value: quote(prompt.tool.Name)}, protocol.JSONField{Name: "toolResult", Value: raw(result)}))}
	if decision.Behavior == "allow" {
		updated := object(result.Get("updatedInput"))
		if len(updated.OwnProperties()) == 0 {
			decision.UpdatedInput = prompt.input
		} else {
			decision.UpdatedInput = &updated
		}
		if result.Get("updatedPermissions") != nil {
			if err = prompt.host.ApplyNativePermissionUpdates(scope, result.Get("updatedPermissions")); err != nil {
				return queryloop.ToolPermission{}, err
			}
		}
	} else if string(result.Get("interrupt")) == "true" && prompt.call.CancelModel != nil {
		prompt.call.CancelModel()
	}
	return decision, nil

}
func (prompt *NativeSDKPermissionPrompt) Cancel(scope *cooperative.Scope) error {
	if prompt == nil || !scope.BelongsTo(prompt.host.options.Owner) {
		return cooperative.ErrRestricted
	}
	h := prompt.host
	if h.sdkPermissions == nil || h.sdkPermissions.pending[prompt.requestID] == nil {
		return nil
	}
	delete(h.sdkPermissions.pending, prompt.requestID)
	h.forgetSDKPermissionRequest(prompt.requestID)
	err := h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_cancel_request")}, protocol.JSONField{Name: "request_id", Value: quote(prompt.requestID)}))
	failure := prompt.future.Reject(scope, &NativeSDKCallbackError{Name: "AbortError"})
	if err != nil {
		return err
	}
	return failure
}
func (prompt *NativeSDKPermissionPrompt) Finish(scope *cooperative.Scope) error {
	if prompt == nil || !scope.BelongsTo(prompt.host.options.Owner) {
		return cooperative.ErrRestricted
	}
	if prompt.stop != nil {
		prompt.stop()
	}
	if prompt.host.sdkPermissions != nil {
		delete(prompt.host.sdkPermissions.pending, prompt.requestID)
	}
	prompt.host.forgetSDKPermissionRequest(prompt.requestID)
	return nil
}
func (h *Host) BeginNativeSDKPermissionPrompt(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (*NativeSDKPermissionPrompt, error) {
	if h == nil || ctx == nil || !scope.BelongsTo(h.options.Owner) || input == nil || h.options.Print == nil || h.options.Print.PermissionPromptToolName != "stdio" {
		return nil, cooperative.ErrRestricted
	}
	prompt := &NativeSDKPermissionPrompt{host: h, context: ctx, tool: tool, input: input, call: call, requestID: h.options.UUID()}
	var err error
	prompt.future, err = NewOwnerFuture[protocol.JSONObject](h.options.Owner)
	if err != nil {
		return nil, err
	}
	if h.sdkPermissions == nil || !h.sdkPermissions.open {
		err = prompt.future.Reject(scope, errors.New("Stream closed"))
		return prompt, err
	}
	h.sdkPermissions.pending[prompt.requestID] = prompt
	prompt.stop = context.AfterFunc(ctx, func() {
		_, _ = h.options.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return prompt.Cancel(next) })
	})
	request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("can_use_tool")}, protocol.JSONField{Name: "tool_name", Value: quote(prompt.tool.Name)}, protocol.JSONField{Name: "input", Value: raw(prompt.input)}, protocol.JSONField{Name: "tool_use_id", Value: prompt.call.ToolUseID})
	if prompt.call.PermDecision != nil {
		request.Set("permission_suggestions", prompt.call.PermDecision.Suggestions)
		reason := object(prompt.call.PermDecision.DecisionReason)
		switch reason.String("type") {
		case "hook", "asyncAgent", "sandboxOverride", "workingDir", "safetyCheck", "other":
			request.Set("decision_reason", reason.Get("reason"))
		}
	}
	if agent := sdkPermissionAgentID(prompt.call); agent != "" {
		request.Set("agent_id", quote(agent))
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_request")}, protocol.JSONField{Name: "request_id", Value: quote(prompt.requestID)}, protocol.JSONField{Name: "request", Value: raw(request)})
	h.trackSDKPermissionRequest(event)
	if err = h.enqueueSDKControl(scope, event); err != nil {
		delete(h.sdkPermissions.pending, prompt.requestID)
		h.forgetSDKPermissionRequest(prompt.requestID)
		_ = prompt.future.Reject(scope, err)
		return prompt, nil
	}

	return prompt, nil
}
func (h *Host) ResolveNativeSDKPermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || input == nil || h.options.Print == nil || h.options.Print.PermissionPromptToolName != "stdio" {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	has, err := h.hooks.NativeEventPresent("PermissionRequest", h.options.Controller.SessionID())
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if has {
		return h.resolveNativeSDKPermissionHooks(ctx, scope, tool, input, call)
	}
	prompt, err := h.BeginNativeSDKPermissionPrompt(ctx, scope, tool, input, call)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	defer prompt.Finish(scope)
	value, err := prompt.RawFuture().Await(scope)
	return prompt.Complete(scope, value, err)
}
