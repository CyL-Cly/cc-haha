package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// Every Source queryModel consumer updates the same process quota state. Side
// queries retain their own model authority and cancellation, while forwarding
// the existing native host's same-owner response consumer.
func (h *Host) nativeQuotaConsumer() func(*cooperative.Scope, *provider.BunHeaders, error) error {
	if h == nil || h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Controller == nil || !h.options.Controller.BootstrapOwns(h.options.Owner) {
		return nil
	}
	return h.options.Print.NativeQuotaStatus
}
