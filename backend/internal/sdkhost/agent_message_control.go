package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) agentMessageControl(scope *cooperative.Scope, input protocol.JSONObject) error {
	if !scope.BelongsTo(h.options.Owner) || h.options.SendAgentMessage == nil {
		return cooperative.ErrRestricted
	}
	request := object(input.Get("request"))
	if request.String("subtype") != "send_agent_message" {
		return cap("Agent message control subtype")
	}
	value, e := h.options.SendAgentMessage(scope, request.String("agent_id"), request.String("content"))
	response := protocol.NewJSONObject()
	if e != nil {
		response.Set("subtype", quote("error"))
		response.Set("request_id", input.Get("request_id"))
		response.Set("error", quote(e.Error()))
	} else {
		response.Set("subtype", quote("success"))
		response.Set("request_id", input.Get("request_id"))
		response.Set("response", raw(value))
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})
	data, e := protocol.EncodeNDJSON(event)
	if e != nil {
		return e
	}
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
