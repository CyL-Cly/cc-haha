package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (h *Host) nativeThinkingSummaryBeta(scope *cooperative.Scope, model string) (*bool, error) {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.NativeThinkingSummaryBeta == nil {
		return nil, nil
	}
	value, err := h.options.Print.NativeThinkingSummaryBeta(scope, model)
	return &value, err
}

func (h *Host) nativeThinkingSummaryBetaCallback() func(*cooperative.Scope, string) (*bool, error) {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.NativeThinkingSummaryBeta == nil {
		return nil
	}
	return h.nativeThinkingSummaryBeta
}
