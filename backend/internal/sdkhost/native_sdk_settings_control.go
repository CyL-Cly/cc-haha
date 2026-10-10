package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) sdkGetSettingsControl(scope *cooperative.Scope, event protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKGetSettings == nil {
		return cooperative.ErrRestricted
	}
	value, err := h.options.NativeSDKGetSettings(scope)
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	if err != nil {
		response.Set("subtype", quote("error"))
		response.Set("error", quote(err.Error()))
	} else {
		response.Set("response", raw(value))
	}
	return h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
}
