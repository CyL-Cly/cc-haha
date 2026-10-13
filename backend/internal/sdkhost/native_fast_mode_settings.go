package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sameSettingsOutsideFastModeRemoval(previous, next *protocol.JSONObject) bool {
	if previous == nil || next == nil || next.Get("fastMode") != nil {
		return false
	}
	left := previous.Clone()
	left.Delete("fastMode")
	return sameSettingsOutsideModelSelection(&left, next)
}

// The owned fast rejection producer may accept only its exact removal of
// fastMode. It neither reloads permissions nor authorizes another setting edit.
func (h *Host) AcceptInternalFastModeSettings(scope *cooperative.Scope) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.options.NativeFastMode == nil || h.options.Print.NativeTypedModelSettings == nil || !h.options.Print.NativeTypedModelSettings(scope) {
		return cooperative.ErrRestricted
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	next, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if !sameSettingsOutsideFastModeRemoval(h.internalSettingsBaseline(previous), next) {
		return cap("internal fast setting changed another consumer")
	}
	h.internalFastSettings = next
	return h.certifyInternalSettings(previous, next)
}
func (h *Host) sameInternalModelSettings(previous, next *protocol.JSONObject) bool {
	return sameSettingsOutsideModelSelection(previous, next) || h.internalFastSettings != nil && sameSettingsOutsideFastModeRemoval(previous, h.internalFastSettings) && sameSettingsOutsideModelSelection(h.internalFastSettings, next)
}
