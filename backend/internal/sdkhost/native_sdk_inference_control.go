package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func (h *Host) sdkInferenceControl(scope *cooperative.Scope, event protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKInferenceControl == nil {
		return cooperative.ErrRestricted
	}
	if err := h.options.NativeSDKInferenceControl(scope, object(event.Get("request"))); err != nil {
		return err
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	return h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
}

func (h *Host) SetNativeSDKThinking(scope *cooperative.Scope, thinking requestcontext.Thinking) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	h.options.Print.Thinking = thinking
	return nil
}
