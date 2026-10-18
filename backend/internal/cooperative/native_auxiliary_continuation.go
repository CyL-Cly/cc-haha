package cooperative

import "context"

// The actual Web/Insights async functions have independent continuations after
// local credential stat as well as HTTP. A cloned synchronous scope would nest
// those awaits on one Go stack and prevent the outer Promise.all siblings from
// reaching their sockets. Reuse the Owner's sealed token scheduler; the only
// admitted authority remains the original typed producer, never a generic Read
// callback, SideQuestion identity, new ASK, or separate Owner.
func runParkedNativeAuxiliary(owner *Owner, caller *Scope, ctx context.Context, produce func(*Scope) error, web *NativeAuxiliaryModels, insights *NativeInsightsModels) error {
	if caller == nil || !caller.BelongsTo(owner) || ctx == nil || produce == nil || (web == nil) == (insights == nil) || web != nil && web.owner != owner || insights != nil && !insights.Owns(owner) {
		return ErrRestricted
	}
	done := make(chan struct{})
	var failure error
	delegate := &NativeSideQuestionModels{owner: owner}
	lane, err := delegate.createContinuation(ctx, produce, func(_ *Scope, err error) { failure = err; close(done) }, false)
	if err != nil {
		return err
	}
	// Preserve the distinct purpose proofs. A Web/Insights continuation cannot
	// present a child SideQuestion identity or derive permission-hook authority.
	lane.authority = nil
	lane.auxiliaryAuthority = web
	lane.insightsAuthority = insights
	if err = lane.advance(); err != nil {
		return err
	}
	if err = caller.AwaitPromise(done); err != nil {
		return err
	}
	return failure
}

func (scope *Scope) isNativeAwaitingAuxiliary(owner *Owner) bool {
	if scope == nil || !scope.BelongsTo(owner) || scope.sideContinuation == nil {
		return false
	}
	lane := scope.sideContinuation
	return lane.owner == owner && lane.executing.Load() && (lane.auxiliaryAuthority != nil && lane.auxiliaryAuthority.owner == owner || lane.insightsAuthority.Owns(owner))
}
