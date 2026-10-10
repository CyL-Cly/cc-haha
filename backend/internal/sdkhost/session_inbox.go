package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

func (h *Host) sessionInboxStopControl(scope *cooperative.Scope, input protocol.JSONObject, cancelModel context.CancelFunc) error {
	if !scope.BelongsTo(h.options.Owner) || !h.sessionInbox {
		return cooperative.ErrRestricted
	}
	if _, e := h.options.Controller.CancelSessionInboxOnOwner(scope); e != nil {
		return e
	}
	if cancelModel != nil {
		cancelModel()
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: input.Get("request_id")})
	return h.enqueueInboxFIFO(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
}

// InitializeSessionInbox follows transcript activation so history deduplication
// sees the loaded session, including resume and fork startup.
func (h *Host) InitializeSessionInbox(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.isAgent() {
		return nil
	}
	if h.sessionInbox {
		return cooperative.ErrRestricted
	}
	e := h.options.Controller.BindSessionInboxOnOwner(scope, func(receipt protocol.JSONObject) error {
		event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("session_message_receipt")})
		event.Assign(receipt)
		event.Set("source_uuid", quote(sessioninbox.MessageUUID(receipt.Get("message_id"))))
		event.Set("session_id", quote(h.options.Controller.SessionID()))
		event.Set("uuid", quote(h.options.UUID()))
		return h.enqueueInboxFIFO(event)
	})
	if e == nil {
		h.sessionInbox = true
	}
	return e
}
func (h *Host) enqueueInboxFIFO(event protocol.JSONObject) error {
	data, e := protocol.EncodeNDJSON(event)
	if e != nil {
		return e
	}
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
func (h *Host) AcceptSessionMessage(scope *cooperative.Scope, request protocol.JSONObject) (protocol.JSONObject, error) {
	if !scope.BelongsTo(h.options.Owner) || !h.sessionInbox || h.closing {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	uuid, e := sessioninbox.MessageUUIDChecked(request.Get("message_id"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	persisted, e := h.options.Controller.SessionMessagePersistedOnOwner(scope, uuid)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return h.options.Controller.AcceptSessionMessageOnOwner(scope, request, persisted)
}
func (h *Host) sessionInboxControl(scope *cooperative.Scope, input protocol.JSONObject) (bool, error) {
	request := object(input.Get("request"))
	result, e := h.AcceptSessionMessage(scope, request)
	response := protocol.NewJSONObject()
	response.Set("subtype", quote("success"))
	response.Set("request_id", input.Get("request_id"))
	if e != nil {
		response.Set("subtype", quote("error"))
		response.Set("error", quote(e.Error()))
	} else {
		response.Set("response", raw(result))
	}
	return e == nil, h.enqueueInboxFIFO(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
}
