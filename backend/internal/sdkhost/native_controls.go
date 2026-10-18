package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (h *Host) SetNativePermissionMode(scope *cooperative.Scope, mode string) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	if err := h.options.Controller.SetSDKPermissionMode(scope, mode); err != nil {
		return err
	}
	h.permissionMode = mode
	return nil
}

func (h *Host) RestoreNativeRemotePermissionMode(scope *cooperative.Scope, mode string) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	if err := h.options.Controller.RestoreRemotePermissionMode(scope, mode); err != nil {
		return err
	}
	actual, err := h.options.Controller.PlanModeContext(scope)
	if err != nil {
		return err
	}
	h.permissionMode = actual.String("mode")
	return nil
}

func (h *Host) EnterNativePlanMode(scope *cooperative.Scope) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	if e := h.options.Controller.EnterPlanMode(scope); e != nil {
		return e
	}
	h.permissionMode = "plan"
	return nil
}
func (h *Host) ExitNativePlanMode(scope *cooperative.Scope) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	if e := h.options.Controller.ExitPlanMode(scope); e != nil {
		return e
	}
	actual, e := h.options.Controller.PlanModeContext(scope)
	if e != nil {
		return e
	}
	h.permissionMode = actual.String("mode")
	return nil
}

func (h *Host) ReloadNativeSettings(scope *cooperative.Scope, rules map[string]sdkpermissions.RuleSet) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	settings, err := h.options.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if err = h.options.Controller.ReloadSDKSettings(scope, settings, rules); err != nil {
		return err
	}
	h.internalModelSettings = nil
	h.clearInternalSettingsCertificate()
	return h.initializePrintCatalog(scope)
}

func (h *Host) RefreshNativeToolCatalog(scope *cooperative.Scope) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.nativeQueryTools != nil {
		h.nativeQueryTools.pending = true
		return nil
	}
	return h.initializePrintCatalog(scope)
}
