package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Restore is Source's bootstrap model override assignment. It also admits the
// original explicit empty string; SDK model commands retain their own guards.
func (h *Host) RestoreNativeRemoteInferenceModel(scope *cooperative.Scope, resolved string) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	h.options.Model = resolved
	return nil
}
