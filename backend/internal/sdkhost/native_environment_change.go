package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// SetNativeRequestEnvironment replaces the process view, never mutating a
// map borrowed by an in-flight HTTP worker. The next request uses the same
// Host, model controller, credentials and shared TLS state.
func (h *Host) SetNativeRequestEnvironment(scope *cooperative.Scope, environment map[string]string) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if h.options.Print == nil || !h.options.Print.NativeProcess {
		return cooperative.ErrRestricted
	}
	request, err := NativePrintRequestEnvironment(environment)
	if err != nil {
		return err
	}
	policy, err := producerPolicyFromOriginalEnvironment(environment, true)
	if err != nil {
		return err
	}
	original := map[string]string{}
	for key, value := range environment {
		original[key] = value
	}
	h.options.OriginalEnv, h.requestEnvironment, h.streamPolicy = original, request, policy
	return nil
}
