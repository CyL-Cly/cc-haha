package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AdmitSessionInput is the same async parsed SDK producer as print's stdin.
// A duplicate owns its replay acknowledgement but never a new ASK or abort.
func (h *Host) AdmitSessionInput(scope *cooperative.Scope, input protocol.JSONObject) (bool, error) {
	future, err := h.AdmitParsedSDK(scope, input)
	if err != nil {
		return false, err
	}
	admission, err := future.Await(scope)
	if err != nil {
		return false, err
	}
	if admission.Acknowledgement != nil {
		if _, err = h.enqueue(scope, *admission.Acknowledgement); err != nil {
			return false, err
		}
	}
	if admission.Historical {
		h.options.OnPhase("lifecycle:completed")
	}
	return !admission.Duplicate, nil
}
