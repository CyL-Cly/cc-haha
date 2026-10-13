package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) enqueueAuthStatus(scope *cooperative.Scope, status cloudauth.AuthStatus) error {
	if h.closing {
		return nil
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("auth_status")}, protocol.JSONField{Name: "isAuthenticating", Value: raw(status.IsAuthenticating)}, protocol.JSONField{Name: "output", Value: raw(status.Output)}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())})
	if status.Error != nil {
		message.Set("error", quote(*status.Error))
	}
	return h.enqueueSDKControl(scope, message)
}
func (h *Host) subscribeAuthStatus() error {
	if h.options.AuthStatus == nil {
		return cap("auth status actual cloud refresh manager")
	}
	h.authStatusDispose = h.options.AuthStatus.Subscribe(func(status cloudauth.AuthStatus) {
		// Child-process readers cannot retain an owner while the owner is waiting
		// for that same process. FIFO admission is asynchronous, like Source emit.
		_, _ = h.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { return h.enqueueAuthStatus(scope, status) })
	})
	return nil
}
