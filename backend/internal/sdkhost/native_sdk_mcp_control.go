package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) sdkMCPMessageControl(scope *cooperative.Scope, event protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.options.NativeSDKMCPMessage == nil {
		return cooperative.ErrRestricted
	}
	request := object(event.Get("request"))
	if err := h.options.NativeSDKMCPMessage(scope, request.String("server_name"), object(request.Get("message"))); err != nil {
		return err
	}
	// The original input loop ACKs unknown, failed and pending SDK names too.
	// Only the bound runtime may find and deliver to an existing live client.
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	return h.enqueueSDKControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
}
