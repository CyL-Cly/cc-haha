package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

type nativeSDKHookState struct {
	open, initialized  bool
	pending            map[string]*OwnerFuture[protocol.JSONObject]
	permissionOrder    []string
	permissionRequests map[string]protocol.JSONObject
}

func (h *Host) bindNativeSDKHookInput(scope *cooperative.Scope, input *PlainStructuredInput) error {
	if h.options.NativeSDKHookInitialize == nil || h.options.Print == nil || !h.options.Print.NativeProcess {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if h.sdkHooks == nil {
		h.sdkHooks = &nativeSDKHookState{pending: map[string]*OwnerFuture[protocol.JSONObject]{}}
	}
	h.sdkHooks.open = true
	if h.options.NativeSDKMCP != nil {
		if err := h.options.NativeSDKMCP.bind(scope, h.enqueueSDKControl, h.options.UUID); err != nil {
			return err
		}
	}
	if err := h.hooks.BindNativeSDKCallbacks(scope, &queryhooks.CallbackBindings{Owner: h.options.Owner, Run: h.runNativeSDKHookCallback}); err != nil {
		return err
	}
	input.allowSDKInitialize = true
	input.allowSDKMCP = h.options.NativeSDKMCPMessage != nil
	input.allowSDKMCPControl = h.options.NativeSDKMCPControl != nil
	input.allowSDKGetSettings = h.options.NativeSDKGetSettings != nil
	input.allowSDKMCPStatus = h.options.NativeSDKMCPStatus != nil
	input.allowSDKInference = h.options.NativeSDKInferenceControl != nil
	input.allowSDKPermissionMode = h.options.NativeSDKPermissionControl != nil
	replay, e := h.options.Controller.ReplayUserMessagesOnOwner(scope)
	if e != nil {
		return e
	}
	oldResponse, oldClose := input.sandboxResponse, input.sandboxClose
	input.sandboxResponse = func(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
		input.sandboxReplay = replay
		if uuid := message.String("uuid"); uuid != "" && h.sessionInbox {
			if e := h.options.Controller.ConsumeSessionMessageOnOwner(scope, uuid); e != nil {
				return false, e
			}
		}
		if h.options.NativeSDKMCP != nil {
			if handled, err := h.options.NativeSDKMCP.response(scope, message); handled || err != nil {
				return handled, err
			}
		}
		if handled, err := h.nativeSDKHookResponse(scope, message); handled || err != nil {
			return handled, err
		}
		if oldResponse != nil {
			if handled, err := oldResponse(scope, message); handled || err != nil {
				return handled, err
			}
		}
		// Source silently consumes orphan/late hook responses without replay.
		input.sandboxReplay = false
		return true, nil
	}
	input.sandboxClose = func(scope *cooperative.Scope) error {
		if err := h.closeNativeSDKHookInput(scope); err != nil {
			return err
		}
		if oldClose != nil {
			return oldClose(scope)
		}
		return nil
	}
	return nil
}
func (h *Host) sdkHookError(scope *cooperative.Scope, id string, err error) error {
	if h.options.NativeSDKHookError != nil {
		return h.options.NativeSDKHookError(scope, id, err)
	}
	return nil
}
func (h *Host) runNativeSDKHookCallback(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (protocol.JSONObject, error) {
	scope := call.Scope
	if !scope.BelongsTo(h.options.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	empty := protocol.NewJSONObject()
	failed := func(err error) (protocol.JSONObject, error) {
		if e := h.sdkHookError(scope, sdkCallbackID(hook.Get("callback_id")), err); e != nil {
			return empty, e
		}
		return empty, nil
	}
	if h.sdkHooks == nil || !h.sdkHooks.open {
		return failed(errors.New("Stream closed"))
	}
	if ctx.Err() != nil {
		return failed(errors.New("Request aborted"))
	}
	requestID := h.options.UUID()
	future, err := NewOwnerFuture[protocol.JSONObject](h.options.Owner)
	if err != nil {
		return empty, err
	}
	h.sdkHooks.pending[requestID] = future
	stop := context.AfterFunc(ctx, func() {
		_, _ = h.options.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error {
			if h.sdkHooks.pending[requestID] == nil {
				return nil
			}
			if e := h.enqueueSDKControl(next, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_cancel_request")}, protocol.JSONField{Name: "request_id", Value: quote(requestID)})); e != nil {
				return e
			}
			delete(h.sdkHooks.pending, requestID)
			return future.Reject(next, &NativeSDKCallbackError{Name: "AbortError"})
		})
	})
	defer stop()
	defer delete(h.sdkHooks.pending, requestID)
	request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("hook_callback")}, protocol.JSONField{Name: "callback_id", Value: hook.Get("callback_id")}, protocol.JSONField{Name: "input", Value: raw(input)})
	if call.HookID != "" {
		request.Set("tool_use_id", quote(call.HookID))
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_request")}, protocol.JSONField{Name: "request_id", Value: quote(requestID)}, protocol.JSONField{Name: "request", Value: raw(request)})
	if err = h.enqueueSDKControl(scope, event); err != nil {
		return empty, err
	}
	value, err := future.Await(scope)
	if err != nil {
		if errors.Is(err, cooperative.ErrClosed) {
			return empty, err
		}
		return failed(err)
	}
	return value, nil
}
func (h *Host) enqueueSDKControl(scope *cooperative.Scope, event protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	data, err := protocol.EncodeNDJSON(event)
	if err != nil {
		return err
	}
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
func (h *Host) nativeSDKHookResponse(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	if h.sdkHooks == nil {
		return false, nil
	}
	response := object(message.Get("response"))
	id := response.String("request_id")
	future := h.sdkHooks.pending[id]
	if future == nil {
		return false, nil
	}
	delete(h.sdkHooks.pending, id)
	if response.String("subtype") == "error" {
		return true, future.Reject(scope, errors.New(response.String("error")))
	}
	schema, err := queryhooks.HookOutputSchema()
	if err != nil {
		return true, err
	}
	parsed, issues := pluginschema.Validate(schema, response.Get("response"))
	if len(issues) > 0 {
		return true, future.Reject(scope, &NativeSDKCallbackError{Name: "ZodError", Message: pluginschema.ErrorJSON(issues, nil)})
	}
	var result protocol.JSONObject
	if err = result.UnmarshalJSON(parsed); err != nil {
		return true, err
	}
	return true, future.Resolve(scope, result)
}
func (h *Host) closeNativeSDKHookInput(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if h.sdkHooks == nil {
		return nil
	}
	h.sdkHooks.open = false
	if h.options.NativeSDKMCP != nil {
		if err := h.options.NativeSDKMCP.close(scope); err != nil {
			return err
		}
	}
	for id, future := range h.sdkHooks.pending {
		delete(h.sdkHooks.pending, id)
		if err := future.Reject(scope, errors.New("Tool permission stream closed before response received")); err != nil {
			return err
		}
	}
	return nil
}
func (h *Host) sdkHookInitializeControl(scope *cooperative.Scope, event protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.sdkHooks == nil || h.options.NativeSDKHookInitialize == nil {
		return cooperative.ErrRestricted
	}
	request := object(event.Get("request"))
	if value := request.Get("sdkMcpServers"); value != nil {
		if h.options.NativeSDKMCP != nil {
			if err := h.options.NativeSDKMCP.RegisterInitializeConfigurations(scope, value); err != nil {
				return err
			}
		} else {
			names, err := sdkMCPInitializeNames(value)
			if err != nil {
				return err
			}
			if len(names) > 0 {
				return cap("SDK MCP actual config/control producer")
			}
		}
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	firstInitialize := !h.sdkHooks.initialized
	if h.sdkHooks.initialized {
		response.Set("subtype", quote("error"))
		response.Set("error", quote("Already initialized"))
		response.Set("pending_permission_requests", raw(h.pendingSDKPermissionRequests()))
	} else {
		for _, key := range []string{"promptSuggestions", "agentProgressSummaries"} {
			if request.Get(key) != nil {
				return cap("SDK initialize " + key + " producer")
			}
		}
		if h.options.NativeSDKInitializeOptions != nil {
			if err := h.options.NativeSDKInitializeOptions(scope, request); err != nil {
				return err
			}
		} else {
			for _, key := range []string{"systemPrompt", "appendSystemPrompt", "jsonSchema", "agents"} {
				if request.Get(key) != nil {
					return cap("SDK initialize " + key + " producer")
				}
			}
		}
		metadata, err := h.options.NativeSDKHookInitialize(scope)
		if err != nil {
			return err
		}
		if openaiauth.AuthTruthy(request.Get("hooks")) {
			hooks := object(request.Get("hooks"))
			if !hooks.IsObject() {
				return cap("SDK hook declaration object")
			}
			if err = h.hooks.RegisterSDKWorktreeCallbacks(scope, hooks); err != nil {
				return err
			}
		}
		h.sdkHooks.initialized = true
		response.Set("response", raw(metadata))
	}
	if err := h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})); err != nil {
		return err
	}
	if firstInitialize && h.options.EnableAuthStatus {
		return h.enqueueAuthStatus(scope, h.options.AuthStatus.Status())
	}
	return nil
}

func (h *Host) pendingSDKPermissionRequests() []protocol.JSONObject {
	result := []protocol.JSONObject{}
	if h.sdkHooks != nil {
		for _, id := range h.sdkHooks.permissionOrder {
			if value, ok := h.sdkHooks.permissionRequests[id]; ok {
				result = append(result, value.Clone())
			}
		}
	}
	return result
}
func (h *Host) trackSDKPermissionRequest(event protocol.JSONObject) {
	if h.sdkHooks == nil {
		return
	}
	if h.sdkHooks.permissionRequests == nil {
		h.sdkHooks.permissionRequests = map[string]protocol.JSONObject{}
	}
	id := event.String("request_id")
	h.sdkHooks.permissionOrder = append(h.sdkHooks.permissionOrder, id)
	h.sdkHooks.permissionRequests[id] = event.Clone()
}
func (h *Host) forgetSDKPermissionRequest(id string) {
	if h.sdkHooks == nil {
		return
	}
	delete(h.sdkHooks.permissionRequests, id)
	for i, value := range h.sdkHooks.permissionOrder {
		if value == id {
			h.sdkHooks.permissionOrder = append(h.sdkHooks.permissionOrder[:i], h.sdkHooks.permissionOrder[i+1:]...)
			break
		}
	}
}

// NativeSDKCallbackError retains the original rejection class at the console
// diagnostic boundary; successful HookJSONOutput still uses the shared schema.
type NativeSDKCallbackError struct{ Name, Message string }

func (e *NativeSDKCallbackError) Error() string { return e.Message }
func sdkCallbackID(raw json.RawMessage) string {
	value, e := protocol.ExactJavaScriptStringJSON(raw)
	if e != nil {
		return ""
	}
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}
