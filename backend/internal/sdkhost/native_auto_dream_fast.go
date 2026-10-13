package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (h *Host) bindAutoDreamFastMode(scope *cooperative.Scope, input requestretry.Input) (requestretry.Input, error) {
	if !h.nativeAutoDreamModels.Current(scope) {
		return requestretry.Input{}, cooperative.ErrRestricted
	}
	binding, err := h.nativeFastModeBinding(scope)
	if err != nil {
		return requestretry.Input{}, err
	}
	var proof *cooperative.NativeFastModeRequest
	if binding != nil {
		proof = binding.Proof
	}
	input.Context.Host.NativeFastModeProof = proof
	input.ResolvedFastMode, err = h.options.Controller.ResolvedFastModeOnOwner(scope, proof)
	return input, err
}
