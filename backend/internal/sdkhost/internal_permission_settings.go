package sdkhost

import (
	"bytes"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sameSettingsOutsidePermissionPersistence(previous, next *protocol.JSONObject) bool {
	if previous == nil || next == nil || !previous.IsObject() || !next.IsObject() {
		return false
	}
	remaining := func(value *protocol.JSONObject) protocol.JSONObject {
		copy := value.Clone()
		permissions, ok := copy.Object("permissions")
		if ok {
			for _, key := range []string{"allow", "deny", "ask", "additionalDirectories", "defaultMode"} {
				permissions.Delete(key)
			}
			if len(permissions.OwnProperties()) == 0 {
				copy.Delete("permissions")
			} else {
				raw, _ := permissions.MarshalJSON()
				copy.Set("permissions", raw)
			}
		}
		return copy
	}
	left, right := remaining(previous), remaining(next)
	fields := map[string][]byte{}
	for _, p := range left.OwnProperties() {
		fields[string(p.NameJSON)] = protocol.StringifyValidatedJavaScriptJSON(p.Value)
	}
	if len(fields) != len(right.OwnProperties()) {
		return false
	}
	for _, p := range right.OwnProperties() {
		if value, ok := fields[string(p.NameJSON)]; !ok || !bytes.Equal(value, protocol.StringifyValidatedJavaScriptJSON(p.Value)) {
			return false
		}
	}
	return true
}

func (h *Host) internalSettingsBaseline(previous *protocol.JSONObject) *protocol.JSONObject {
	if h.internalOwnerSettingsBase == previous && h.internalOwnerSettings != nil {
		if !settingsMatchSnapshot(previous, h.internalOwnerSettingsBaseSnapshot) {
			return nil
		}
		var snapshot protocol.JSONObject
		if snapshot.UnmarshalJSON(h.internalOwnerSettingsSnapshot) == nil {
			return &snapshot
		}
		return nil
	}
	return previous
}

func settingsMatchSnapshot(settings *protocol.JSONObject, snapshot []byte) bool {
	if settings == nil || snapshot == nil {
		return false
	}
	raw, err := settings.MarshalJSON()
	return err == nil && bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), protocol.StringifyValidatedJavaScriptJSON(snapshot))
}

// Cache identity alone is insufficient: JSONObject exposes shared references,
// including nested values. Keep detached raw bytes of both proven objects.
func (h *Host) certifyInternalSettings(previous, next *protocol.JSONObject) error {
	baseRaw, err := previous.MarshalJSON()
	if err != nil {
		return err
	}
	nextRaw, err := next.MarshalJSON()
	if err != nil {
		return err
	}
	h.internalOwnerSettingsBase, h.internalOwnerSettings = previous, next
	h.internalOwnerSettingsBaseSnapshot = append([]byte(nil), baseRaw...)
	h.internalOwnerSettingsSnapshot = append([]byte(nil), nextRaw...)
	return nil
}

func (h *Host) clearInternalSettingsCertificate() {
	h.internalModelSettings, h.internalFastSettings = nil, nil
	h.internalOwnerSettings, h.internalOwnerSettingsBase = nil, nil
	h.internalOwnerSettingsSnapshot, h.internalOwnerSettingsBaseSnapshot = nil, nil
}

// Internal permission writes suppress the disk notifier in Source. Certify only
// the exact owner-performed edit; retain the current in-memory session rules.
func (h *Host) acceptInternalPermissionSettings(scope *cooperative.Scope, before *protocol.JSONObject) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	if err = h.validateASKSettingsReference(previous, before, scope); err != nil {
		return err
	}
	next, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if !sameSettingsOutsidePermissionPersistence(before, next) {
		return cap("internal permission persistence changed another consumer")
	}
	if err = h.validateInternalNativeSettings(scope, next); err != nil {
		return err
	}
	return h.certifyInternalSettings(previous, next)
}

// get_settings resets the Source cache to read disk, even when nothing has
// changed. A value-identical owned refresh retains the proven consumers. The
// constructor-bound SDK directory reader also certifies its actual fresh disk
// result; unbound changed settings still require the real notifier or edit.
func (h *Host) AcceptNativeSettingsReadRefresh(scope *cooperative.Scope, before *protocol.JSONObject) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	if err = h.validateASKSettingsReference(previous, before, scope); err != nil {
		return nil
	}
	next, err := h.nativeSDKInitialSettings()
	if err != nil {
		return err
	}
	oldRaw, _ := before.MarshalJSON()
	newRaw, _ := next.MarshalJSON()
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(oldRaw), protocol.StringifyValidatedJavaScriptJSON(newRaw)) && h.nativeSDKSettingsReader == nil {
		return nil
	}
	if err = h.validateInternalNativeSettings(scope, next); err != nil {
		return err
	}
	return h.certifyInternalSettings(previous, next)
}
