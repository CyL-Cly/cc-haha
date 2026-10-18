package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (h *Host) nativeAgentSummaryFork(scope *cooperative.Scope) (*Host, func(*cooperative.Scope) error, error) {
	if h.agentSummary == nil || !h.agentSummary.models.Current(scope) {
		return nil, nil, cooperative.ErrRestricted
	}
	fork := *h
	print := *h.options.Print
	release := func(*cooperative.Scope) error { return nil }
	if print.Tools != nil {
		tools, cleanup, err := print.Tools.WithDeniedToolFork(scope)
		if err != nil {
			return nil, nil, err
		}
		release = cleanup
		_, implementations, err := tools.Catalog(scope)
		if err != nil {
			_ = release(scope)
			return nil, nil, err
		}
		fork.registered = append([]queryloop.RegisteredTool{}, h.registered...)
		for i, original := range fork.registered {
			for _, replacement := range implementations {
				if original.Name == replacement.Name {
					fork.registered[i] = replacement
					break
				}
			}
		}
		print.Tools = tools
	}
	print.AgentID, print.QuerySource = "", "agent_summary"
	print.NativeAutoDream, print.CloseNativeAutoDream = nil, nil
	print.NativeAgentSummary = nil
	print.NativeTeammateStop, print.NativeLeaderInbox = nil, nil
	fork.options.Print = &print
	fork.askSkillOverrides = nil
	fork.nativePermissionHookContext = nil
	return &fork, release, nil
}

func (h *Host) bindNativeAgentSummaryFastMode(scope *cooperative.Scope, input requestretry.Input) (requestretry.Input, error) {
	if h.agentSummary == nil || !h.agentSummary.models.Current(scope) {
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
