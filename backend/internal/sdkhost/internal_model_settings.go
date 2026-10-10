package sdkhost

import (
	"bytes"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// The settings notifier suppresses internal writes in Source. ModelPicker's
// model/effort persistence can invalidate getInitialSettings without replacing
// AppState.settings or reloading its toolPermissionContext. Other setting
// changes require the real applySettingsChange subscriber, not this transition.
func sameSettingsOutsideModelSelection(previous, next *protocol.JSONObject) bool {
	if previous == nil || next == nil || !previous.IsObject() || !next.IsObject() {
		return false
	}
	remaining := func(object *protocol.JSONObject) map[string][]byte {
		fields := map[string][]byte{}
		for _, property := range object.OwnProperties() {
			if bytes.Equal(property.NameJSON, []byte(`"model"`)) || bytes.Equal(property.NameJSON, []byte(`"effortLevel"`)) {
				continue
			}
			if property.Value == nil {
				fields[string(property.NameJSON)] = nil
			} else {
				fields[string(property.NameJSON)] = protocol.StringifyValidatedJavaScriptJSON(property.Value)
			}
		}
		return fields
	}
	left, right := remaining(previous), remaining(next)
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, present := right[key]
		if !present || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

// AcceptInternalModelSettings is called by the actual owner-bound ModelPicker
// after its internal disk writes. It reads the existing cache itself; callers
// cannot supply a prepared settings object or replace permissions/AppState.
func (h *Host) AcceptInternalModelSettings(scope *cooperative.Scope) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.options.Controller == nil || h.options.Startup == nil {
		return cap("internal model settings require original shared services")
	}
	previous, _, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return err
	}
	next, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if err := sdkpermissions.ValidateNativeWithBindings(next, h.options.Print.Registry.HasNativePluginBindings(), h.options.Print.NativeMCP != nil, h.options.Print.Tools != nil && h.options.Print.Tools.HasWebFetchBindings(), h.options.Print.Tools != nil && h.options.Print.Tools.HasWebSearchBindings(), h.options.Print.Permissions.NativeStartup.Owns(h.options.Owner), h.options.Print.Tools != nil && h.options.Print.Tools.HasWorktreeBindings(), h.options.Print.Hooks.HasNativeCommandBindings(h.options.Owner), h.options.Print.NativeSystemContext != nil, nativeTeamExecutionBound(scope, h.options), nativeAuthHelperBound(scope, h.options), nativeCloudSettingsBound(scope, h.options), nativeWorkflowKeywordBound(h.options), h.options.Print.Tools != nil && h.options.Print.Tools.HasInternalReadableBindings(), h.options.Print.Tools != nil && h.options.Print.Tools.HasSandboxBindings(), h.options.Print.NativeTypedModelSettings != nil && h.options.Print.NativeTypedModelSettings(scope), h.options.Print.NativeMainAgentSettings != nil && h.options.Print.NativeMainAgentSettings(scope)); err != nil {
		return err
	}
	if !h.sameInternalModelSettings(previous, next) {
		return cap("internal model settings changed another consumer")
	}
	h.internalModelSettings = next
	return nil
}

func (h *Host) validateASKSettingsReference(settings, merged *protocol.JSONObject, scopes ...*cooperative.Scope) error {
	var scope *cooperative.Scope
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	if merged == settings {
		return nil
	}
	if h.validateTransferredASKSettings(settings, merged) {
		return nil
	}
	if h.options.Print == nil || !h.options.Print.NativeProcess || (merged != h.internalModelSettings && merged != h.internalFastSettings) || !h.sameInternalModelSettings(settings, merged) {
		return cap("ASK settings cache identity changed")
	}
	return sdkpermissions.ValidateNativeWithBindings(merged, h.options.Print.Registry.HasNativePluginBindings(), h.options.Print.NativeMCP != nil, h.options.Print.Tools != nil && h.options.Print.Tools.HasWebFetchBindings(), h.options.Print.Tools != nil && h.options.Print.Tools.HasWebSearchBindings(), h.options.Print.Permissions.NativeStartup.Owns(h.options.Owner), h.options.Print.Tools != nil && h.options.Print.Tools.HasWorktreeBindings(), h.options.Print.Hooks.HasNativeCommandBindings(h.options.Owner), h.options.Print.NativeSystemContext != nil, nativeTeamExecutionBound(scope, h.options), nativeAuthHelperBound(scope, h.options), nativeCloudSettingsBound(scope, h.options), nativeWorkflowKeywordBound(h.options), h.options.Print.Tools != nil && h.options.Print.Tools.HasInternalReadableBindings(), h.options.Print.Tools != nil && h.options.Print.Tools.HasSandboxBindings(), h.options.Print.NativeTypedModelSettings != nil && h.options.Print.NativeTypedModelSettings(scope), h.options.Print.NativeMainAgentSettings != nil && h.options.Print.NativeMainAgentSettings(scope))
}
