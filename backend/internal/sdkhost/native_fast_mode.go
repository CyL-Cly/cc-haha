package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

type NativeFastModeBinding struct {
	Proof  *cooperative.NativeFastModeRequest
	State  *retrypolicy.FastState
	Effect func(retrypolicy.FastEffect) error
}

// A cache-safe parent context retains data, never a completed request's
// authority. Source's shared getAppState is sampled in each actual fork round.
func (h *Host) bindSideQuestionFastMode(scope *cooperative.Scope, input requestretry.Input) (requestretry.Input, error) {
	if !h.nativeSideQuestionModels.Current(scope) {
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

func (h *Host) nativeFastModeBinding(scope *cooperative.Scope) (*NativeFastModeBinding, error) {
	if h.options.NativeFastMode == nil {
		return nil, nil
	}
	value, err := h.options.NativeFastMode(scope)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if !value.Proof.Owns(h.options.Owner) || value.State == nil || value.Effect == nil {
		return nil, cooperative.ErrRestricted
	}
	return value, nil
}
