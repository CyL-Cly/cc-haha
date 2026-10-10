package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) contextUsageControl(scope *cooperative.Scope, input protocol.JSONObject) error {
	if h.options.Print == nil || h.options.Print.NativeContextUsage == nil {
		return cap("original context analyser binding")
	}
	request := object(input.Get("request"))
	value, failure := h.options.Print.NativeContextUsage(scope, hookRawTruthy(request.Get("estimateOnly")))
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: input.Get("request_id")})
	if failure != nil {
		response.Set("subtype", quote("error"))
		response.Set("error", quote(failure.Error()))
	} else {
		response.Set("response", raw(value))
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})
	data, err := protocol.EncodeNDJSON(message)
	if err != nil {
		return err
	}
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
