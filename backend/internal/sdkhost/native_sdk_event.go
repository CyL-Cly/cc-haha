package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// EmitNativeSDKEvent retains the canonical owner output FIFO. Native mode
// observers must not write synchronously to a remote socket from the Owner.
func (h *Host) EmitNativeSDKEvent(scope *cooperative.Scope, event protocol.JSONObject) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	return h.enqueueSDKControl(scope, event)
}
