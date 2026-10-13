package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (h *Host) ApplyNativePermissionUpdates(scope *cooperative.Scope, data json.RawMessage) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	updates := sdkpermissions.ParsePermissionUpdates(data)
	before, e := h.options.Startup.GetInitialSettings()
	if e != nil {
		return e
	}
	var persist func(protocol.JSONObject) error
	if h.options.Print.PersistPermission != nil {
		persist = func(update protocol.JSONObject) error { return h.options.Print.PersistPermission(scope, update) }
	}
	if e := h.options.Controller.ApplySDKPermissionUpdates(scope, updates, persist); e != nil {
		return e
	}
	if e := h.acceptInternalPermissionSettings(scope, before); e != nil {
		return e
	}
	actual, e := h.options.Controller.PlanModeContext(scope)
	if e != nil {
		return e
	}
	h.permissionMode = actual.String("mode")
	return nil
}
func (h *Host) NativePermissionContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if h.nativePermissionView != nil {
		if !scope.BelongsTo(h.options.Owner) {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		context, err := h.nativePermissionView(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		return h.overlaySkillPermission(context)
	}
	if h.options.NativeParentSettings != nil {
		_, context, err := h.options.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		return h.overlaySkillPermission(context)
	}
	context, err := h.options.Controller.PlanModeContext(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return h.overlaySkillPermission(context)
}
