package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Then observes the existing flush promise without waiting for it. The returned
// dependent promise settles after its same-owner reaction, never on an I/O
// worker. Administrative owner close rejects only that immutable obligation.
func (future *FlushFuture) Then(scope *cooperative.Scope, run func(*cooperative.Scope, error) error) (*ownerasync.OwnerFuture[struct{}], error) {
	if future == nil || future.future == nil || !scope.BelongsTo(future.owner) || run == nil {
		return nil, cooperative.ErrRestricted
	}
	var completion *ownerasync.OwnerFuture[struct{}]
	err := scope.RegisterPromiseReaction(func(owner *cooperative.Owner) error {
		// Register supplies the validated, non-nil canonical Owner. The
		// constructor's sole error is a nil Owner.
		completion, _ = ownerasync.NewOwnerFuture[struct{}](owner)
		// RejectAdministrative below receives this allocated future and only
		// non-nil errors; its argument-validation rejection is unreachable.
		return future.future.thenOwner(owner, func(next *cooperative.Scope, failure error) {
			if err := run(next, failure); err != nil {
				if rejected := completion.Reject(next, err); rejected != nil {
					_ = completion.RejectAdministrative(err)
				}
				return
			}
			// thenOwner posts onto a fresh owner-lifetime scope, independent of
			// the caller's ASK context. Concurrent Owner.Close may still cancel
			// that scope during the callback; settle its obligation immutably.
			if err := completion.Resolve(next, struct{}{}); err != nil {
				_ = completion.RejectAdministrative(err)
			}
		}, func(failure error) { _ = completion.RejectAdministrative(failure) })
	})
	if err != nil && completion != nil {
		_ = completion.RejectAdministrative(err)
		return completion, nil
	}
	return completion, err
}
