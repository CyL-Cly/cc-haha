package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) stopTaskControl(scope *cooperative.Scope, input protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.options.StopTask == nil {
		return cooperative.ErrRestricted
	}
	request := object(input.Get("request"))
	if request.String("subtype") != "stop_task" {
		return cap("background task control subtype")
	}
	result, e := h.options.StopTask(scope, request.String("task_id"))
	response := protocol.NewJSONObject()
	if e != nil {
		response.Set("subtype", quote("error"))
		response.Set("request_id", input.Get("request_id"))
		response.Set("error", quote(e.Error()))
	} else if string(result.Get("ok")) != "true" {
		response.Set("subtype", quote("error"))
		response.Set("request_id", input.Get("request_id"))
		response.Set("error", result.Get("message"))
	} else {
		response.Set("subtype", quote("success"))
		response.Set("request_id", input.Get("request_id"))
		payload := protocol.NewJSONObject()
		if string(result.Get("alreadyGone")) == "true" {
			payload.Set("stopped", raw(false))
			payload.Set("reason", quote("not_found"))
		}
		response.Set("response", raw(payload))
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})
	data, e := protocol.EncodeNDJSON(event)
	if e != nil {
		return e
	}
	// Source control responses use the outbound FIFO directly. They neither
	// participate in ASK consumer receipts nor replace its last result.
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
