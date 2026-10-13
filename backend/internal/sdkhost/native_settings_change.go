package sdkhost

import (
	"bytes"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (h *Host) ApplyNativeSettingsChange(scope *cooperative.Scope, rules map[string]sdkpermissions.RuleSet) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	next, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if err = h.options.Controller.ApplyNativeSettingsChange(scope, next, rules); err != nil {
		return err
	}
	if next != nil && h.options.Print != nil {
		newEffort := next.Get("effortLevel")
		var oldEffort []byte
		if previous != nil {
			oldEffort = previous.Get("effortLevel")
		}
		var normalizedOld []byte
		if len(oldEffort) > 0 {
			normalizedOld = protocol.StringifyValidatedJavaScriptJSON(oldEffort)
		}
		if newEffort != nil && !bytes.Equal(normalizedOld, protocol.StringifyValidatedJavaScriptJSON(newEffort)) {
			h.options.Print.EffortValue = append([]byte{}, newEffort...)
		}
	}
	_, permission, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	h.permissionMode = permission.String("mode")
	h.internalModelSettings = nil
	h.clearInternalSettingsCertificate()
	return h.initializePrintCatalog(scope)
}
