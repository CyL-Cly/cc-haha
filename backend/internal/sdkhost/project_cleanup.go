package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

// StartCleanup enters Source's registered async Project callback synchronously
// through flush admission, then returns that callback's pending promise. It
// does not await, join outer Record work, run a model or own process exit.
func (h *Host) StartCleanup(scope *cooperative.Scope) (*runtimecleanup.Future, error) {
	if h == nil || h.pending == nil || h.options.Controller == nil || h.options.OnPhase == nil || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	h.closing = true
	if err := h.closeNativeSDKPermissionInput(scope); err != nil {
		return h.rejectedCleanup(scope, err)
	}
	if err := h.closeNativeSDKHookInput(scope); err != nil {
		return h.rejectedCleanup(scope, err)
	}
	if h.sessionInbox {
		if err := h.options.Controller.DisposeSessionInboxOnOwner(scope); err != nil {
			return h.rejectedCleanup(scope, err)
		}
	}
	flush, err := h.pending.Flush(scope)
	if err != nil {
		// Source's callback is async: even a synchronous flush failure rejects
		// its returned promise instead of throwing out of the registry map.
		return h.rejectedCleanup(scope, err)
	}
	completion, err := flush.Then(scope, func(_ *cooperative.Scope, failure error) error {
		if failure != nil {
			return failure
		}
		h.options.OnPhase("metadata:shutdown-reappend")
		// The Source try/catch covers metadata alone, after fulfilled flush.
		_ = h.options.Controller.Writer().RefreshMetadata()
		return nil
	})
	if err != nil {
		// Admission already happened. A caller canceled by Flush's native
		// timer-clear cannot turn this async callback into a synchronous throw.
		return h.rejectedCleanup(scope, err)
	}
	return completion, nil
}

func (h *Host) rejectedCleanup(scope *cooperative.Scope, failure error) (*runtimecleanup.Future, error) {
	// StartCleanup has already validated this non-nil canonical Owner.
	completion, _ := ownerasync.NewOwnerFuture[struct{}](h.options.Owner)
	if err := completion.Reject(scope, failure); err != nil {
		// No metadata or Writer mutation occurs off owner: only the admitted
		// callback obligation settles, retaining the original rejection.
		// This API rejects only nil receivers/reasons; both are established
		// above, so immutable settlement cannot fail here.
		_ = completion.RejectAdministrative(failure)
	}
	return completion, nil
}
