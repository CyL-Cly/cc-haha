package sdkhost

import (
	"bytes"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sameSettingsOutsidePluginCacheEdit(before, after *protocol.JSONObject) bool {
	return sameSettingsOutsidePluginCacheProducer(before, after, "")
}

func sameSettingsOutsidePluginCacheProducer(before, after *protocol.JSONObject, uninstallID string) bool {
	if before == nil || after == nil || !before.IsObject() || !after.IsObject() {
		return false
	}
	beforeForComparison := before
	if uninstallID != "" {
		oldConfigs, oldObject := before.Object("pluginConfigs")
		newConfigs, newObject := after.Object("pluginConfigs")
		if oldObject && newObject && hookRawTruthy(oldConfigs.Get(uninstallID)) && newConfigs.Get(uninstallID) == nil {
			// The exact original last-scope producer deletes one own key via
			// an undefined patch. Its parent and every other key must survive.
			copy := before.Clone()
			oldConfigs = oldConfigs.Clone()
			oldConfigs.Delete(uninstallID)
			copy.Set("pluginConfigs", raw(oldConfigs))
			beforeForComparison = &copy
		}
	}
	remaining := func(value *protocol.JSONObject) map[string][]byte {
		fields := map[string][]byte{}
		for _, property := range value.OwnProperties() {
			if bytes.Equal(property.NameJSON, []byte(`"extraKnownMarketplaces"`)) || bytes.Equal(property.NameJSON, []byte(`"enabledPlugins"`)) {
				continue
			}
			fields[string(property.NameJSON)] = protocol.StringifyValidatedJavaScriptJSON(property.Value)
		}
		return fields
	}
	left, right := remaining(beforeForComparison), remaining(after)
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if next, present := right[key]; !present || !bytes.Equal(value, next) {
			return false
		}
	}
	return true
}

type NativePluginCacheEdit struct {
	host                     *Host
	before                   *protocol.JSONObject
	snapshot                 []byte
	complete                 bool
	uninstallID              string
	optionsID, optionsServer string
}

func (h *Host) BeginNativePluginUninstallCacheEdit(scope *cooperative.Scope, pluginID string) (*NativePluginCacheEdit, error) {
	if pluginID == "" {
		return nil, cooperative.ErrRestricted
	}
	edit, err := h.BeginNativePluginCacheEdit(scope)
	if err == nil {
		edit.uninstallID = pluginID
	}
	return edit, err
}

func (h *Host) BeginNativePluginCacheEdit(scope *cooperative.Scope) (*NativePluginCacheEdit, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.Registry.HasNativePluginBindings() {
		return nil, cooperative.ErrRestricted
	}
	if err := h.checkInferenceOwner(scope); err != nil {
		return nil, err
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return nil, err
	}
	before, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	if err := h.validateASKSettingsReference(previous, before, scope); err != nil {
		return nil, err
	}
	snapshot, err := before.MarshalJSON()
	return &NativePluginCacheEdit{host: h, before: before, snapshot: snapshot}, err
}

// Errors' original clearAllPluginCaches invalidates the getInitialSettings
// pointer after its narrowly scoped plugin declaration edit. Its suppressed
// internal notifier does not replace AppState settings or permission context.
// Certify that actual old cache identity and fresh same-service object; never
// accept caller-prepared settings or unrelated mutable field changes.
func (h *Host) CompleteNativePluginCacheEdit(scope *cooperative.Scope, edit *NativePluginCacheEdit) error {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || edit == nil || edit.host != h || edit.complete || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.Registry.HasNativePluginBindings() {
		return cooperative.ErrRestricted
	}
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	edit.complete = true
	before := edit.before
	if !settingsMatchSnapshot(before, edit.snapshot) {
		return cap("internal plugin edit mutated its original settings cache")
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
	allowed := sameSettingsOutsidePluginCacheProducer(before, next, edit.uninstallID)
	if edit.optionsID != "" {
		allowed = sameSettingsOutsidePluginOptionsProducer(before, next, edit.optionsID, edit.optionsServer)
	}
	if !allowed {
		return cap("internal plugin cache edit changed another consumer")
	}
	if err = h.validateInternalNativeSettings(scope, next); err != nil {
		return err
	}
	return h.certifyInternalSettings(previous, next)
}
