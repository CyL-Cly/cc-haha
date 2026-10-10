package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) startSDKNativeSandboxPermission(ctx context.Context, scope *cooperative.Scope, host string) (*OwnerFuture[bool], error) {
	if !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	future, e := NewOwnerFuture[bool](h.options.Owner)
	if e != nil {
		return nil, e
	}
	if !h.sandboxInputOpen {
		e = future.Resolve(scope, false)
		return future, e
	}
	requestID, toolID := h.options.UUID(), h.options.UUID()
	if h.sandboxPending == nil {
		h.sandboxPending = map[string]*OwnerFuture[bool]{}
	}
	h.sandboxPending[requestID] = future
	stop := context.AfterFunc(ctx, func() {
		_, _ = h.options.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error {
			delete(h.sandboxPending, requestID)
			h.forgetSDKPermissionRequest(requestID)
			return future.Resolve(next, false)
		})
	})
	_, e = future.Then(scope, func(_ *cooperative.Scope, _ bool, _ error) error { stop(); return nil })
	if e != nil {
		stop()
		delete(h.sandboxPending, requestID)
		return nil, e
	}
	request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("can_use_tool")}, protocol.JSONField{Name: "tool_name", Value: quote("SandboxNetworkAccess")}, protocol.JSONField{Name: "input", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "host", Value: quote(host)}))}, protocol.JSONField{Name: "tool_use_id", Value: quote(toolID)}, protocol.JSONField{Name: "description", Value: quote("Allow network connection to " + host + "?")})
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_request")}, protocol.JSONField{Name: "request_id", Value: quote(requestID)}, protocol.JSONField{Name: "request", Value: raw(request)})
	h.trackSDKPermissionRequest(event)
	_, e = h.enqueue(scope, event)
	if e != nil {
		delete(h.sandboxPending, requestID)
		h.forgetSDKPermissionRequest(requestID)
		_ = future.Resolve(scope, false)
	}
	return future, e
}
func sandboxPermissionBehavior(response protocol.JSONObject) bool {
	stringField := func(name string, required bool) bool {
		value := response.Get(name)
		if value == nil {
			return !required
		}
		var s string
		return json.Unmarshal(value, &s) == nil
	}
	if !stringField("toolUseID", false) {
		return false
	}
	switch response.String("behavior") {
	case "allow":
		var input protocol.JSONObject
		return response.Get("updatedInput") != nil && input.UnmarshalJSON(response.Get("updatedInput")) == nil && input.IsObject()
	case "deny":
		if !stringField("message", true) {
			return false
		}
		if value := response.Get("interrupt"); value != nil {
			var b bool
			if json.Unmarshal(value, &b) != nil {
				return false
			}
		}
		return false
	default:
		return false
	}
}
func (h *Host) nativeSandboxControlResponse(scope *cooperative.Scope, message protocol.JSONObject) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	response := object(message.Get("response"))
	id := response.String("request_id")
	future := h.sandboxPending[id]
	if future == nil {
		return false, nil
	}
	delete(h.sandboxPending, id)
	h.forgetSDKPermissionRequest(id)
	allowed := false
	if response.String("subtype") != "error" {
		allowed = sandboxPermissionBehavior(object(response.Get("response")))
	}
	return true, future.Resolve(scope, allowed)
}
func (h *Host) closeNativeSandboxInput(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	h.sandboxInputOpen = false
	for id, future := range h.sandboxPending {
		delete(h.sandboxPending, id)
		h.forgetSDKPermissionRequest(id)
		if e := future.Resolve(scope, false); e != nil {
			return e
		}
	}
	return nil
}

func (h *Host) bindNativeSandboxInput(scope *cooperative.Scope, input *PlainStructuredInput) error {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.Tools == nil || !h.options.Print.Tools.HasSandboxBindings() {
		return nil
	}
	h.sandboxInputOpen = true
	input.sandboxResponse = h.nativeSandboxControlResponse
	replay, e := h.options.Controller.ReplayUserMessagesOnOwner(scope)
	if e != nil {
		return e
	}
	input.sandboxReplay = replay
	input.sandboxClose = h.closeNativeSandboxInput
	return nil
}

// Context is the owning runtime lifetime; losing a proxy client does not abort
// an outstanding SDK permission. RunStream EOF supplies the original rejection.
