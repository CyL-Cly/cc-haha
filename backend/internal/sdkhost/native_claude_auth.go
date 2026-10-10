package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (h *Host) nativeClaudeProof(scope *cooperative.Scope) *cooperative.NativeClaudeRequest {
	if h.options.NativeClaudeAuth == nil {
		return nil
	}
	binding, err := h.options.NativeClaudeAuth(scope)
	if err != nil {
		return nil
	}
	return binding.Proof(h.options.Owner, scope)
}
