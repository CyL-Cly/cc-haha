package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ApplyNativeSessionModel is the native SDK control consumer, with the same
// owner and main-controller identity as the next ASK.
func (h *Host) ApplyNativeSessionModel(scope *cooperative.Scope, requested, resolved, display string) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.options.Controller == nil || h.isAgent() {
		return cooperative.ErrRestricted
	}
	if resolved == "" {
		return cap("empty resolved native inference model")
	}
	rows, err := h.options.Controller.InjectSessionModelBreadcrumbs(scope, requested, display)
	if err != nil {
		return err
	}
	if err := h.SetInferenceModel(scope, resolved, nil, false); err != nil {
		return err
	}
	crumb := rows[2]
	_, err = h.enqueue(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: crumb.Get("message")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)}, protocol.JSONField{Name: "uuid", Value: crumb.Get("uuid")}, protocol.JSONField{Name: "timestamp", Value: crumb.Get("timestamp")}, protocol.JSONField{Name: "isReplay", Value: raw(true)}))
	return err
}
