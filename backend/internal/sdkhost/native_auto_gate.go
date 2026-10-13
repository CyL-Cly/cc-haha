package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (h *Host) VerifyNativeAutoGate(scope *cooperative.Scope, cliFlag bool) (string, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return "", cooperative.ErrRestricted
	}
	notice, err := h.options.Controller.VerifyNativeAutoGate(scope, cliFlag)
	if err != nil {
		return "", err
	}
	context, err := h.NativePermissionContext(scope)
	if err == nil {
		h.permissionMode = context.String("mode")
	}
	return notice, err
}
