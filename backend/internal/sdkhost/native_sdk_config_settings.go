package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BindNativeSDKSettingsReader borrows only the original native factory's
// settings graph. A JSON settings object or arbitrary read callback cannot
// stand in for the constructor-bound reader or this Host's current owner.
func (h *Host) BindNativeSDKSettingsReader(scope *cooperative.Scope, reader *clistartupenv.NativeSDKSettingsReader) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.closing || h.isAgent() || h.options.Print == nil || !h.options.Print.NativeProcess || !reader.UsesSettingsContext(h.options.Startup, h.options.Settings, h.options.SettingsState, h.options.Environment) || !h.options.Controller.UsesOriginalSettingsContext(h.options.Startup, h.options.Settings, h.options.SettingsState, h.options.Environment) {
		return cooperative.ErrRestricted
	}
	if h.nativeSDKSettingsReader != nil && h.nativeSDKSettingsReader != reader {
		return cooperative.ErrRestricted
	}
	h.nativeSDKSettingsReader = reader
	return nil
}

func (h *Host) nativeSDKInitialSettings() (*protocol.JSONObject, error) {
	if reader := h.nativeSDKSettingsReader; reader != nil {
		return reader.InitialSettings()
	}
	return h.options.Startup.GetInitialSettings()
}
