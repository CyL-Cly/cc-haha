package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeSDKMCPControlResult carries only ACK data and the changed-name refresh
// decision. Transport setup belongs after the shared output FIFO admission.
type NativeSDKMCPControlResult struct {
	Response   protocol.JSONObject
	RefreshSDK bool
}

func (h *Host) sdkMCPReconcileControl(scope *cooperative.Scope, event protocol.JSONObject) (bool, error) {
	if !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKMCPControl == nil {
		return false, cooperative.ErrRestricted
	}
	result, failure := h.options.NativeSDKMCPControl(scope, object(event.Get("request")))
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	if failure != nil {
		response.Set("subtype", quote("error"))
		response.Set("error", quote(failure.Error()))
	} else if result.Response.IsObject() {
		response.Set("response", raw(result.Response))
	}
	if err := h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})); err != nil {
		return false, err
	}
	return failure == nil && result.RefreshSDK, nil
}

// ReplaceConfigurations mutates the single SDK configuration object. Callers
// preserve same-name configs exactly, as handleMcpSetServers does.
func (s *NativeSDKMCPState) ReplaceConfigurations(scope *cooperative.Scope, configurations protocol.JSONObject) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.configurations = configurations.Clone()
	return nil
}

// The ACK consumer can observe completion of the exact post-ACK producer
// without starting a second updateSdkMcp pass.
func (h *Host) NativeSDKMCPControlRefresh(scope *cooperative.Scope) (*OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	return h.sdkMCPControlRefresh, nil
}
func (h *Host) refreshSDKMCPAfterControl(scope *cooperative.Scope) (failure error) {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	future, err := NewOwnerFuture[struct{}](h.options.Owner)
	if err != nil {
		return err
	}
	h.sdkMCPControlRefresh = future
	defer func() {
		if h.sdkMCPControlRefresh == future {
			h.sdkMCPControlRefresh = nil
		}
		if failure != nil {
			_ = future.Reject(scope, failure)
		} else {
			_ = future.Resolve(scope, struct{}{})
		}
	}()
	return h.options.NativeSDKPrepareTurn(scope)
}
